/*
 * Применение поколения: кандидат рядом с боевым файлом, `haproxy -c` на нём,
 * атомарная подмена, SIGUSR2 мастеру.
 *
 * Порядок важен: боевой файл не трогается, пока -c не сказал «валиден», —
 * упавший кандидат оставляет и файл, и процесс прежними. Reload после подмены
 * не рвёт соединения: мастер в режиме -W перечитывает конфиг и поднимает
 * новый воркер, отставляя старый дорабатывать свои соединения.
 *
 * Сигнал уходит только мастеру, готовому его принять. Свой обработчик SIGUSR2
 * мастер ставит последним шагом загрузки, а до того сигнал либо смертелен,
 * либо теряется (haproxy 3.0.26, проверено на образе стенда):
 *
 *   разбор конфига       — действие по умолчанию: мастер умирает (код 140);
 *   pidfile уже записан  — сигнал заблокирован и игнорируется: reload молча
 *                          пропадает, мастер остаётся на старом файле;
 *   цикл мастера         — перехвачен: reload.
 *
 * То же окно открывается на каждом reload: мастер перезапускает себя
 * (execvp) с заблокированными сигналами и заново проходит загрузку. Поэтому
 * «pidfile есть и kill 0 проходит» — ещё не готовность. Готовность — SIGUSR2
 * в маске перехваченных сигналов мастера (SigCgt в /proc/<pid>/status).
 */

package apply

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DefaultWait — сколько Reload ждёт готовности мастера, если Config.Wait не
// задан. Загрузка мастера — доли секунды; дольше — уже не гонка старта, а
// беда самого мастера, и её разбирает повтор в desired.Watch.
const DefaultWait = 15 * time.Second

// readyPoll — шаг, с которым Reload переспрашивает готовность мастера.
const readyPoll = 100 * time.Millisecond

// ErrMasterDown — мастер сигнал не примет: pidfile ещё нет или он чужой,
// мастер грузится, перечитывает конфиг или уже умер. Это состояние процесса,
// а не файла: пройдёт само, и применение стоит повторить. Отказ `haproxy -c`
// сюда не относится.
var ErrMasterDown = errors.New("haproxy master is not ready")

type Config struct {
	// Bin — бинарь haproxy: им валидируется кандидат.
	Bin string
	// CfgPath — боевой файл, который читает мастер.
	CfgPath string
	// PidFile — куда мастер записал свой pid (haproxy -W -p <файл>).
	PidFile string
	// MasterPid — pid мастера, которого поднял entrypoint (0 — неизвестен).
	// pidfile с другим номером — не наш, и сигнал туда не уходит.
	MasterPid int
	// Wait — сколько ждать готовности мастера перед сигналом (0 — DefaultWait).
	Wait time.Duration
}

// Apply валидирует и устанавливает файл, затем перечитывает мастер. Ошибка с
// ErrMasterDown значит, что файл уже на месте и не хватило только сигнала.
func Apply(ctx context.Context, cfg Config, text string) error {
	candidate := cfg.CfgPath + ".next"

	if err := os.MkdirAll(filepath.Dir(cfg.CfgPath), 0o755); err != nil {
		return fmt.Errorf("conf dir: %w", err)
	}

	if err := os.WriteFile(candidate, []byte(text), 0o644); err != nil {
		return fmt.Errorf("write candidate: %w", err)
	}

	out, err := exec.Command(cfg.Bin, "-c", "-f", candidate).CombinedOutput()
	if err != nil {
		// Кандидат оставляется на диске: это улика для разбора, не мусор.
		return fmt.Errorf("haproxy -c: %w: %s", err, firstLines(string(out), 4))
	}

	if err := os.Rename(candidate, cfg.CfgPath); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	if err := Reload(ctx, cfg); err != nil {
		return fmt.Errorf("reload: %w", err)
	}

	return nil
}

// Reload ждёт, пока мастер будет готов (не дольше cfg.Wait), и шлёт ему
// SIGUSR2: тот перечитывает конфиг без разрыва соединений.
func Reload(ctx context.Context, cfg Config) error {
	pid, err := waitReady(ctx, cfg)
	if err != nil {
		return err
	}
	if err := syscall.Kill(pid, syscall.SIGUSR2); err != nil {
		return fmt.Errorf("%w: signal master %d: %w", ErrMasterDown, pid, err)
	}
	return nil
}

// Ready — pid мастера, который примет SIGUSR2, или ErrMasterDown с причиной.
func Ready(cfg Config) (int, error) {
	pid, err := ourMaster(cfg)
	if err != nil {
		return 0, err
	}

	st, err := readStatus(pid)
	if err != nil {
		return 0, fmt.Errorf("%w: master %d: %w", ErrMasterDown, pid, err)
	}
	if st.dead() {
		return 0, fmt.Errorf("%w: master %d is dead (%s)", ErrMasterDown, pid, st.state)
	}
	if !st.catches(syscall.SIGUSR2) {
		return 0, fmt.Errorf("%w: master %d has no SIGUSR2 handler yet", ErrMasterDown, pid)
	}

	return pid, nil
}

// Alive — жив ли мастер. Мягче Ready: на время reload мастер жив, но сигнал
// не примет, и пульс не должен мигать на каждом перечитывании.
func Alive(cfg Config) bool {
	pid, err := ourMaster(cfg)
	if err != nil {
		return false
	}
	st, err := readStatus(pid)
	return err == nil && !st.dead()
}

// MasterPid — первая строка pidfile: в режиме -W мастер пишет туда себя.
func MasterPid(pidFile string) (int, error) {
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, fmt.Errorf("pidfile: %w", err)
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(raw)), "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("pidfile %s: bad pid %q", pidFile, line)
	}
	return pid, nil
}

// ourMaster — pid из pidfile, если это мастер, которого поднял entrypoint.
func ourMaster(cfg Config) (int, error) {
	pid, err := MasterPid(cfg.PidFile)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrMasterDown, err)
	}
	if cfg.MasterPid > 0 && pid != cfg.MasterPid {
		return 0, fmt.Errorf("%w: pidfile names %d, entrypoint started %d",
			ErrMasterDown, pid, cfg.MasterPid)
	}
	return pid, nil
}

// waitReady переспрашивает Ready, пока мастер не будет готов или не выйдет
// срок; на сроке отдаёт последнюю причину.
func waitReady(ctx context.Context, cfg Config) (int, error) {
	wait := cfg.Wait
	if wait <= 0 {
		wait = DefaultWait
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	tick := time.NewTicker(readyPoll)
	defer tick.Stop()

	for {
		pid, err := Ready(cfg)
		if err == nil {
			return pid, nil
		}

		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("%w (waited %s)", err, time.Since(start).Round(time.Millisecond))
		case <-tick.C:
		}
	}
}

// procStatus — то, что решает готовность, из /proc/<pid>/status: состояние
// процесса и маска перехваченных сигналов.
type procStatus struct {
	state  string
	sigCgt uint64
}

func readStatus(pid int) (procStatus, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return procStatus{}, err
	}

	var st procStatus
	for _, line := range strings.Split(string(raw), "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)

		switch key {
		case "State":
			st.state = val
		case "SigCgt":
			if st.sigCgt, err = strconv.ParseUint(val, 16, 64); err != nil {
				return procStatus{}, fmt.Errorf("SigCgt %q: %w", val, err)
			}
		}
	}

	return st, nil
}

// dead — процесс завершился, но не прибран. Мастер — ребёнок агента
// (entrypoint запускает его и exec'ом становится агентом), а агент своих
// детей не ждёт: умерший мастер остаётся зомби, и kill 0 считает его живым.
func (st procStatus) dead() bool {
	return strings.HasPrefix(st.state, "Z") || strings.HasPrefix(st.state, "X")
}

// catches — стоит ли у процесса свой обработчик sig (бит sig-1 в SigCgt).
func (st procStatus) catches(sig syscall.Signal) bool {
	return st.sigCgt&(1<<(uint(sig)-1)) != 0
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " | ")
}
