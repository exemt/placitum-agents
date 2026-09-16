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

const DefaultWait = 15 * time.Second

const readyPoll = 100 * time.Millisecond

var ErrMasterDown = errors.New("haproxy master is not ready")

type Config struct {
	Bin       string
	CfgPath   string
	PidFile   string
	MasterPid int
	Wait      time.Duration
}

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

func Alive(cfg Config) bool {
	pid, err := ourMaster(cfg)
	if err != nil {
		return false
	}
	st, err := readStatus(pid)
	return err == nil && !st.dead()
}

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

func (st procStatus) dead() bool {
	return strings.HasPrefix(st.state, "Z") || strings.HasPrefix(st.state, "X")
}

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
