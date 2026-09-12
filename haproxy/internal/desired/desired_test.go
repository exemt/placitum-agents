package desired

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/exemt/placitum-agents/haproxy/internal/apply"
)

func confJSON(t *testing.T, cfg string, mangleHash bool) []byte {
	t.Helper()
	sum := sha256.Sum256([]byte(cfg))
	hash := "sha256:" + hex.EncodeToString(sum[:])
	if mangleHash {
		hash = "sha256:" + strings.Repeat("0", 64)
	}
	raw, err := json.Marshal(map[string]any{
		"v":      1,
		"kind":   "haproxy-conf",
		"rev":    3,
		"sha256": hash,
		"cfg":    cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseConf(t *testing.T) {
	cfg := "global\n    maxconn 128\n"

	conf, err := ParseConf(confJSON(t, cfg, false))
	if err != nil {
		t.Fatalf("valid conf rejected: %v", err)
	}
	if conf.Rev != 3 || conf.Cfg != cfg {
		t.Fatalf("parsed wrong: rev=%d cfg=%q", conf.Rev, conf.Cfg)
	}
}

func TestParseConfHashMismatch(t *testing.T) {
	if _, err := ParseConf(confJSON(t, "global\n", true)); err == nil {
		t.Fatal("mangled hash accepted")
	}
}

func TestParseConfWrongKind(t *testing.T) {
	raw := []byte(`{"v":1,"kind":"agent-conf","rev":1,"sha256":"sha256:00","cfg":"x"}`)
	if _, err := ParseConf(raw); err == nil {
		t.Fatal("foreign kind accepted")
	}
}

// entry — запись KV из теста: цикл канала смотрит только в значение и
// операцию.
type entry struct {
	value []byte
}

func (e entry) Bucket() string                  { return Bucket }
func (e entry) Key() string                     { return ConfKey }
func (e entry) Value() []byte                   { return e.value }
func (e entry) Revision() uint64                { return 0 }
func (e entry) Created() time.Time              { return time.Time{} }
func (e entry) Delta() uint64                   { return 0 }
func (e entry) Operation() jetstream.KeyValueOp { return jetstream.KeyValuePut }

func put(t *testing.T, rev int, cfg string) jetstream.KeyValueEntry {
	t.Helper()
	sum := sha256.Sum256([]byte(cfg))
	raw, err := json.Marshal(map[string]any{
		"v":      1,
		"kind":   "haproxy-conf",
		"rev":    rev,
		"sha256": "sha256:" + hex.EncodeToString(sum[:]),
		"cfg":    cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry{value: raw}
}

// node — применение из теста: отвечает заготовленными ошибками по очереди
// (кончились — успех) и помнит, какие ревизии к нему приходили.
type node struct {
	mu   sync.Mutex
	errs []error
	revs []int
}

func (n *node) applyConf(conf *Conf) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.revs = append(n.revs, conf.Rev)
	if len(n.errs) == 0 {
		return nil
	}
	err := n.errs[0]
	n.errs = n.errs[1:]
	return err
}

func (n *node) calls() []int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.revs)
}

// journal — журнал цикла, который тест читает, пока цикл пишет.
type journal struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (j *journal) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.buf.Write(p)
}

// retries — значения retry_in из строк журнала, по порядку.
func (j *journal) retries(t *testing.T) []string {
	t.Helper()
	j.mu.Lock()
	defer j.mu.Unlock()

	var out []string
	for _, line := range strings.Split(strings.TrimSpace(j.buf.String()), "\n") {
		var rec struct {
			RetryIn string `json:"retry_in"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("journal line %q: %v", line, err)
		}
		if rec.RetryIn != "" {
			out = append(out, rec.RetryIn)
		}
	}
	return out
}

var fast = backoff{first: 10 * time.Millisecond, max: 40 * time.Millisecond}

// masterDown — ошибка применения, как её отдаёт apply на холодном старте.
var masterDown = fmt.Errorf("reload: %w: pidfile: open /var/run/waf/haproxy.pid: no such file or directory",
	apply.ErrMasterDown)

// start запускает цикл канала на канале из теста.
func start(t *testing.T, n *node, pace backoff) (chan<- jetstream.KeyValueEntry, *Applied, *journal) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	updates := make(chan jetstream.KeyValueEntry)
	applied := &Applied{}
	j := &journal{}

	go follow(ctx, updates, n.applyConf, applied, pace, slog.New(slog.NewJSONHandler(j, nil)))

	return updates, applied, j
}

func waitStatus(t *testing.T, applied *Applied, rev int, status string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		gotRev, _, gotStatus := applied.Snapshot()
		if gotRev == rev && gotStatus == status {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("applied %d/%q, want %d/%q", gotRev, gotStatus, rev, status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Холодный старт: мастер ещё грузится. Применение повторяется само, с
// растущим отступом, и сходится в ok без новой рассылки — send той же
// ревизии в KV не придёт.
func TestFollowRetriesWhileMasterDown(t *testing.T) {
	n := &node{errs: []error{masterDown, masterDown, masterDown, masterDown}}
	updates, applied, j := start(t, n, fast)

	updates <- put(t, 5, "global\n")

	waitStatus(t, applied, 5, ApplyOK)
	if got := n.calls(); !slices.Equal(got, []int{5, 5, 5, 5, 5}) {
		t.Fatalf("apply calls %v, want five tries of rev 5", got)
	}
	if got := j.retries(t); !slices.Equal(got, []string{"10ms", "20ms", "40ms", "40ms"}) {
		t.Fatalf("retry_in %v, want doubling up to the cap", got)
	}
}

// Отказ `haproxy -c` — состояние файла: повтором его не исправить, и нода не
// гоняет его по кругу.
func TestFollowRejectionIsFinal(t *testing.T) {
	n := &node{errs: []error{errors.New("haproxy -c: exit status 1: [ALERT] parsing")}}
	updates, applied, j := start(t, n, fast)

	updates <- put(t, 5, "global\n")

	waitStatus(t, applied, 5, ApplyFailed)
	time.Sleep(10 * fast.max)
	if got := n.calls(); !slices.Equal(got, []int{5}) {
		t.Fatalf("apply calls %v, want one", got)
	}
	if got := j.retries(t); len(got) != 0 {
		t.Fatalf("rejected file scheduled for retry: %v", got)
	}
}

// Новая ревизия отменяет повтор старой: встать должна последняя.
func TestFollowNewRevisionCancelsRetry(t *testing.T) {
	n := &node{errs: []error{masterDown}}
	updates, applied, _ := start(t, n, backoff{first: 500 * time.Millisecond, max: time.Second})

	updates <- put(t, 5, "a\n")
	waitStatus(t, applied, 5, ApplyFailed)

	updates <- put(t, 6, "b\n")
	waitStatus(t, applied, 6, ApplyOK)

	time.Sleep(time.Second)
	if got := n.calls(); !slices.Equal(got, []int{5, 6}) {
		t.Fatalf("apply calls %v, want rev 5 once, then rev 6", got)
	}
	waitStatus(t, applied, 6, ApplyOK)
}

// Та же ревизия с тем же хешем — не новое поколение.
func TestFollowSkipsAppliedRevision(t *testing.T) {
	n := &node{}
	updates, applied, _ := start(t, n, fast)

	updates <- put(t, 5, "a\n")
	waitStatus(t, applied, 5, ApplyOK)
	updates <- put(t, 5, "a\n")
	updates <- put(t, 6, "b\n")
	waitStatus(t, applied, 6, ApplyOK)

	if got := n.calls(); !slices.Equal(got, []int{5, 6}) {
		t.Fatalf("apply calls %v, want 5 and 6", got)
	}
}
