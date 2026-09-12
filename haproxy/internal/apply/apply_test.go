package apply

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeMaster — sh, который проходит те же стадии, что мастер haproxy -W (см.
// комментарий пакета): сначала SIGUSR2 по умолчанию, затем pidfile и игнор,
// затем свой обработчик. Каждая стадия длится $HOLD; перехваченный сигнал
// оставляет строку в $MARK.
const fakeMaster = `
sleep "$HOLD"
echo $$ > "$PIDFILE"
trap '' USR2
sleep "$HOLD"
trap 'echo reload >> "$MARK"' USR2
while :; do sleep 1 & wait $!; done
`

type master struct {
	pid     int
	pidFile string
	mark    string
}

func startMaster(t *testing.T, hold string) master {
	t.Helper()

	dir := t.TempDir()
	m := master{
		pidFile: filepath.Join(dir, "haproxy.pid"),
		mark:    filepath.Join(dir, "reloads"),
	}

	cmd := exec.Command("sh", "-c", fakeMaster)
	cmd.Env = append(os.Environ(), "HOLD="+hold, "PIDFILE="+m.pidFile, "MARK="+m.mark)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	m.pid = cmd.Process.Pid
	return m
}

func (m master) config() Config {
	return Config{PidFile: m.pidFile, MasterPid: m.pid, Wait: 5 * time.Second}
}

// Мастер на загрузке сигнал не переживает или теряет. Reload обязан
// дождаться обработчика — тогда перечитывание доезжает, а мастер жив.
func TestReloadWaitsForHandler(t *testing.T) {
	m := startMaster(t, "0.3")

	if err := Reload(context.Background(), m.config()); err != nil {
		t.Fatalf("reload: %v", err)
	}

	waitFor(t, "reload in the handler", func() bool {
		raw, _ := os.ReadFile(m.mark)
		return strings.Contains(string(raw), "reload")
	})
	if !Alive(m.config()) {
		t.Fatal("master did not survive the signal")
	}
}

// Три стадии загрузки — три ответа Ready: нет pidfile, нет обработчика, готов.
func TestReadyFollowsBoot(t *testing.T) {
	m := startMaster(t, "1")
	cfg := m.config()

	if _, err := Ready(cfg); !errors.Is(err, ErrMasterDown) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no pidfile yet: %v", err)
	}

	waitFor(t, "pidfile", func() bool {
		_, err := MasterPid(m.pidFile)
		return err == nil
	})
	if _, err := Ready(cfg); !errors.Is(err, ErrMasterDown) || !strings.Contains(err.Error(), "SIGUSR2") {
		t.Fatalf("pidfile without handler: %v", err)
	}
	if !Alive(cfg) {
		t.Fatal("booting master reported dead")
	}

	waitFor(t, "handler", func() bool {
		pid, err := Ready(cfg)
		return err == nil && pid == m.pid
	})
}

// pidfile, в котором не тот pid, что поднял entrypoint, — не наш мастер, даже
// если процесс жив и сигнал перехватывает.
func TestReadyRefusesForeignPid(t *testing.T) {
	m := startMaster(t, "0.1")
	waitFor(t, "handler", func() bool {
		_, err := Ready(m.config())
		return err == nil
	})

	cfg := m.config()
	cfg.MasterPid = m.pid + 1

	if _, err := Ready(cfg); !errors.Is(err, ErrMasterDown) {
		t.Fatalf("foreign pidfile accepted: %v", err)
	}
	if Alive(cfg) {
		t.Fatal("foreign pidfile reported alive")
	}
}

// Умерший мастер, которого никто не прибрал, — зомби: kill 0 его видит,
// Alive и Ready — нет.
func TestDeadMasterIsNotAlive(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Wait() })

	pid := cmd.Process.Pid
	pidFile := filepath.Join(t.TempDir(), "haproxy.pid")
	writeFile(t, pidFile, strconv.Itoa(pid)+"\n")
	cfg := Config{PidFile: pidFile, MasterPid: pid}

	waitFor(t, "zombie", func() bool {
		st, err := readStatus(pid)
		return err == nil && st.dead()
	})
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("kill 0 on a zombie: %v", err)
	}
	if Alive(cfg) {
		t.Fatal("zombie master reported alive")
	}
	if _, err := Ready(cfg); !errors.Is(err, ErrMasterDown) {
		t.Fatalf("zombie master ready: %v", err)
	}
}

// Отказ `haproxy -c` — не беда мастера: боевой файл не тронут, кандидат
// остаётся уликой, и ErrMasterDown в ошибке нет — повторять нечего.
func TestApplyRejectedIsNotMasterDown(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		Bin:     "false",
		CfgPath: filepath.Join(dir, "haproxy.cfg"),
		PidFile: filepath.Join(dir, "haproxy.pid"),
	}
	writeFile(t, cfg.CfgPath, "old")

	err := Apply(context.Background(), cfg, "new")
	if err == nil || errors.Is(err, ErrMasterDown) {
		t.Fatalf("rejected candidate: %v", err)
	}
	if got := readFile(t, cfg.CfgPath); got != "old" {
		t.Fatalf("live file replaced by a rejected candidate: %q", got)
	}
	if got := readFile(t, cfg.CfgPath+".next"); got != "new" {
		t.Fatalf("candidate not kept: %q", got)
	}
}

// Файл валиден, мастера нет — файл уже на месте, ошибка — ErrMasterDown:
// повтору останется только сигнал.
func TestApplyWithoutMasterIsMasterDown(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		Bin:     "true",
		CfgPath: filepath.Join(dir, "haproxy.cfg"),
		PidFile: filepath.Join(dir, "haproxy.pid"),
		Wait:    300 * time.Millisecond,
	}
	writeFile(t, cfg.CfgPath, "old")

	err := Apply(context.Background(), cfg, "new")
	if !errors.Is(err, ErrMasterDown) {
		t.Fatalf("no master: %v", err)
	}
	if got := readFile(t, cfg.CfgPath); got != "new" {
		t.Fatalf("valid candidate not installed: %q", got)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
