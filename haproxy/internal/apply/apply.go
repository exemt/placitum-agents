package apply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const DefaultWait = 15 * time.Second

// DefaultReloadWait covers name resolution in a large configuration: haproxy answers a reload only
// after it has parsed the file and forked the new worker.
const DefaultReloadWait = 2 * time.Minute

const masterPoll = 100 * time.Millisecond

var ErrMasterDown = errors.New("haproxy master is not ready")

var errNoAnswer = errors.New("master CLI closed the connection without an answer")

type Config struct {
	Bin        string
	CfgPath    string
	PidFile    string
	MasterPid  int
	MasterSock string
	Wait       time.Duration
	ReloadWait time.Duration
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

	if err := os.Remove(loadedPath(cfg)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("loaded mark: %w", err)
	}

	if err := os.Rename(candidate, cfg.CfgPath); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	if err := Reload(ctx, cfg); err != nil {
		return fmt.Errorf("reload: %w", err)
	}

	// Without the mark the next agent start reloads once more, nothing worse.
	_ = os.WriteFile(loadedPath(cfg), []byte(digest(text)+"\n"), 0o644)

	return nil
}

// Running reports whether haproxy already runs text: the live file holds it, the master confirmed
// loading it and still answers.
func Running(ctx context.Context, cfg Config, text string) bool {
	mark, err := os.ReadFile(loadedPath(cfg))
	if err != nil || strings.TrimSpace(string(mark)) != digest(text) {
		return false
	}

	live, err := os.ReadFile(cfg.CfgPath)
	if err != nil || string(live) != text {
		return false
	}

	return waitMaster(ctx, cfg) == nil
}

// Reload reloads haproxy through the master CLI and returns an error unless the master reports the
// new worker started.
func Reload(ctx context.Context, cfg Config) error {
	// The master re-executes itself after start and after every reload and drops connections
	// meanwhile. A reload sent into that gap is lost, so first wait until the master answers.
	if err := waitMaster(ctx, cfg); err != nil {
		return err
	}

	wait := cfg.ReloadWait
	if wait <= 0 {
		wait = DefaultReloadWait
	}

	start := time.Now()
	reloadCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	answer, err := ask(reloadCtx, cfg.MasterSock, "reload")
	if err != nil {
		return fmt.Errorf("%w: no answer to reload: %w (waited %s)",
			ErrMasterDown, err, time.Since(start).Round(time.Millisecond))
	}

	status, startupLog, _ := strings.Cut(answer, "\n")
	switch strings.TrimSpace(status) {
	case "Success=1":
		return nil
	case "Success=0":
		return fmt.Errorf("haproxy failed to load the configuration: %s", alerts(startupLog))
	default:
		return fmt.Errorf("unexpected answer to reload: %s", firstLines(answer, 4))
	}
}

func Alive(cfg Config) bool {
	pid, err := MasterPid(cfg.PidFile)
	if err != nil || (cfg.MasterPid > 0 && pid != cfg.MasterPid) {
		return false
	}
	state, err := procState(pid)
	return err == nil && !dead(state)
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

func waitMaster(ctx context.Context, cfg Config) error {
	wait := cfg.Wait
	if wait <= 0 {
		wait = DefaultWait
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	tick := time.NewTicker(masterPoll)
	defer tick.Stop()

	down := func(err error) error {
		return fmt.Errorf("%w: %w (waited %s)", ErrMasterDown, err, time.Since(start).Round(time.Millisecond))
	}

	for {
		_, err := ask(ctx, cfg.MasterSock, "show proc")
		if err == nil {
			return nil
		}

		// A try that the deadline cuts short would hide the real error behind a timeout.
		if deadline, _ := ctx.Deadline(); time.Until(deadline) < masterPoll {
			return down(err)
		}

		select {
		case <-ctx.Done():
			return down(err)
		case <-tick.C:
		}
	}
}

// ask sends one command to the master CLI and reads the answer up to the master closing the
// connection.
func ask(ctx context.Context, sock, cmd string) (string, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", sock)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	if _, err := io.WriteString(conn, cmd+"\n"); err != nil {
		return "", err
	}
	// Until the write side is closed the master waits for the next command.
	if err := conn.(*net.UnixConn).CloseWrite(); err != nil {
		return "", err
	}

	answer, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}
	if len(answer) == 0 {
		return "", errNoAnswer
	}
	return string(answer), nil
}

func alerts(startupLog string) string {
	var lines []string
	for _, line := range strings.Split(startupLog, "\n") {
		if strings.HasPrefix(line, "[ALERT]") {
			lines = append(lines, strings.Join(strings.Fields(line), " "))
		}
	}
	if len(lines) == 0 {
		return "no alerts in the startup log"
	}
	return strings.Join(lines, " | ")
}

func loadedPath(cfg Config) string {
	return cfg.CfgPath + ".loaded"
}

func digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func procState(pid int) (string, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return "", err
	}

	for _, line := range strings.Split(string(raw), "\n") {
		if state, ok := strings.CutPrefix(line, "State:"); ok {
			return strings.TrimSpace(state), nil
		}
	}

	return "", fmt.Errorf("pid %d: no State in status", pid)
}

func dead(state string) bool {
	return strings.HasPrefix(state, "Z") || strings.HasPrefix(state, "X")
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " | ")
}
