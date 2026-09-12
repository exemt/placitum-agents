/*
 * Конфигурация haproxy с контроллера: KV policy/haproxy-conf.
 *
 * Файл мелкий и не секретный, поэтому лежит в значении ключа целиком — ни
 * блобов в Redis, ни расшифровки, в отличие от поколения nginx. Агент сверяет
 * хеш, проверяет кандидат `haproxy -c` и перечитывает мастер SIGUSR2.
 *
 * Ошибка применения не роняет процесс: «контроллер прислал конфиг, который
 * -c отверг» — это состояние конфигурации. Нода остаётся на прежнем файле и
 * говорит об этом в пульсе, а не падает в рестарт-цикл, унося трафик.
 *
 * Отказ мастера (apply.ErrMasterDown) — другое дело: файл верен, не хватило
 * сигнала, потому что мастер ещё грузится или перечитывает конфиг. Такое
 * применение повторяется с отступом, пока не пройдёт или не придёт новая
 * ревизия. Ждать следующей рассылки нельзя: той же ревизии она не принесёт
 * (send идемпотентен по хешу файла), и холодный старт так и стоял в
 * apply_failed до новой ревизии.
 */

package desired

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/exemt/placitum-agents/haproxy/internal/apply"
)

const (
	Bucket  = "WAF_DESIRED"
	ConfKey = "policy/haproxy-conf"

	ApplyOK     = "ok"
	ApplyFailed = "apply_failed"
)

// retryPace — отступы повтора, сорвавшегося на мастере: первый через секунду,
// дальше вдвое, но не реже раза в полминуты. Каждая попытка и так ждёт
// мастера до apply.DefaultWait, так что частить незачем.
var retryPace = backoff{first: time.Second, max: 30 * time.Second}

type backoff struct {
	first time.Duration
	max   time.Duration
}

type Conf struct {
	V      int    `json:"v"`
	Kind   string `json:"kind"`
	Rev    int    `json:"rev"`
	SHA256 string `json:"sha256"`
	Cfg    string `json:"cfg"`
}

func ParseConf(raw []byte) (*Conf, error) {
	var c Conf
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("haproxy-conf: %w", err)
	}

	if c.V != 1 || c.Kind != "haproxy-conf" {
		return nil, fmt.Errorf("haproxy-conf: unsupported v=%d kind=%q", c.V, c.Kind)
	}
	if c.Rev < 1 {
		return nil, fmt.Errorf("haproxy-conf: rev must be positive")
	}
	if !strings.HasPrefix(c.SHA256, "sha256:") {
		return nil, fmt.Errorf("haproxy-conf: sha256 missing")
	}
	if c.Cfg == "" {
		return nil, fmt.Errorf("haproxy-conf: cfg is empty")
	}

	// Хеш — целостность доставки: файл шёл через KV как часть JSON, и
	// повреждение здесь дешевле ловить сверкой, чем «haproxy -c» на мусоре.
	sum := sha256.Sum256([]byte(c.Cfg))
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != c.SHA256 {
		return nil, fmt.Errorf("haproxy-conf: cfg hash mismatch: %s != %s", got, c.SHA256)
	}

	return &c, nil
}

// Applied — что нода реально применила. Пустая ревизия означает, что
// контроллер поколение ещё не присылал и работает конфиг из образа.
type Applied struct {
	mu     sync.RWMutex
	rev    int
	hash   string
	status string
}

func (a *Applied) Snapshot() (rev int, hash string, status string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.rev, a.hash, a.status
}

func (a *Applied) set(rev int, hash, status string) {
	a.mu.Lock()
	a.rev = rev
	a.hash = hash
	a.status = status
	a.mu.Unlock()
}

// Watch подписывается на policy/haproxy-conf и зовёт applyConf на каждую новую
// ревизию. Первым событием прилетает то, что уже лежит в KV, — нода,
// поднявшаяся после раскатки, получает поколение сразу, а не ждёт следующего.
func Watch(
	ctx context.Context,
	nc *nats.Conn,
	applyConf func(*Conf) error,
	log *slog.Logger,
) (*Applied, error) {
	applied := &Applied{}

	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}

	kv, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:  Bucket,
		History: 5,
	})
	if err != nil {
		return nil, err
	}

	watcher, err := kv.Watch(ctx, ConfKey)
	if err != nil {
		return nil, err
	}

	go func() {
		defer watcher.Stop()
		follow(ctx, watcher.Updates(), applyConf, applied, retryPace, log)
	}()

	return applied, nil
}

// follow — цикл канала. Каждая новая ревизия применяется один раз;
// сорвавшаяся на мастере — ещё раз по таймеру, пока не пройдёт или её не
// сменит новая: на ноде должна встать последняя, а не застрявшая.
func follow(
	ctx context.Context,
	updates <-chan jetstream.KeyValueEntry,
	applyConf func(*Conf) error,
	applied *Applied,
	pace backoff,
	log *slog.Logger,
) {
	var (
		pending *Conf            // ревизия, ждущая повтора
		delay   time.Duration    // через сколько следующий повтор
		retry   <-chan time.Time // nil — повтора нет
	)

	try := func(conf *Conf) {
		pending, retry = nil, nil

		err := applyConf(conf)
		if err == nil {
			applied.set(conf.Rev, conf.SHA256, ApplyOK)
			log.Info("haproxy conf applied", "rev", conf.Rev, "sha256", conf.SHA256)
			return
		}

		applied.set(conf.Rev, conf.SHA256, ApplyFailed)

		if !errors.Is(err, apply.ErrMasterDown) {
			log.Warn("haproxy conf apply failed",
				"rev", conf.Rev,
				"sha256", conf.SHA256,
				"error", err.Error(),
			)
			return
		}

		pending, retry = conf, time.After(delay)
		log.Warn("haproxy conf apply failed",
			"rev", conf.Rev,
			"sha256", conf.SHA256,
			"error", err.Error(),
			"retry_in", delay.String(),
		)
		delay = min(2*delay, pace.max)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-retry:
			try(pending)
		case entry, ok := <-updates:
			if !ok {
				return
			}
			if entry == nil {
				continue
			}

			switch entry.Operation() {
			case jetstream.KeyValueDelete, jetstream.KeyValuePurge:
				continue
			}

			conf, err := ParseConf(entry.Value())
			if err != nil {
				log.Warn("haproxy conf rejected", "error", err.Error())
				continue
			}

			rev, hash, _ := applied.Snapshot()
			if rev == conf.Rev && hash == conf.SHA256 {
				continue
			}

			delay = pace.first
			try(conf)
		}
	}
}
