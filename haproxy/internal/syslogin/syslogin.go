package syslogin

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/exemt/placitum-shared/logkit"
)

const (
	DefaultPath = "/var/run/waf/log.sock"

	MaxBytes = 16 << 10

	readBuffer = 4 << 20

	maxText = 8 << 10
)

var severities = [8]string{
	"emerg", "alert", "crit", "error", "warn", "notice", "info", "debug",
}

func Serve(path string, sink *logkit.Sink, service string) (func() error, error) {
	if path == "" {
		path = DefaultPath
	}

	if sink == nil {
		return nil, fmt.Errorf("syslogin: sink is nil")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	_ = os.Remove(path)

	addr, err := net.ResolveUnixAddr("unixgram", path)
	if err != nil {
		return nil, err
	}

	c, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		return nil, err
	}

	if err := os.Chmod(path, 0o666); err != nil {
		c.Close()
		_ = os.Remove(path)

		return nil, err
	}

	_ = c.SetReadBuffer(readBuffer)

	done := make(chan struct{})

	go func() {
		defer close(done)

		buf := make([]byte, MaxBytes)

		for {
			n, err := c.Read(buf)
			if err != nil {
				return
			}

			if line, ok := Parse(buf[:n], time.Now().UTC(), service); ok {
				sink.Add(line)
			}
		}
	}()

	return func() error {
		err := c.Close()
		<-done
		_ = os.Remove(path)

		return err
	}, nil
}

func Parse(raw []byte, at time.Time, fallback string) (logkit.Line, bool) {
	line := logkit.Line{TS: at, Service: fallback}

	body := strings.TrimRight(string(raw), "\x00\r\n")
	if body == "" {
		return logkit.Line{}, false
	}

	rest := body

	if pri, tail, ok := priority(rest); ok {
		line.Severity = severities[pri%8]
		rest = tail
	}

	if service, tail, ok := head(rest); ok {
		line.Service = service
		rest = tail
	}

	if len(rest) > maxText {
		rest = rest[:maxText]
	}

	line.Text = rest

	return line, rest != ""
}

func priority(s string) (int, string, bool) {
	if len(s) < 3 || s[0] != '<' {
		return 0, s, false
	}

	end := strings.IndexByte(s, '>')
	if end < 2 || end > 4 {
		return 0, s, false
	}

	pri := 0

	for i := 1; i < end; i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, s, false
		}

		pri = pri*10 + int(c-'0')
	}

	if pri > 191 {
		return 0, s, false
	}

	return pri, s[end+1:], true
}

func head(s string) (string, string, bool) {
	rest := s

	for i := 0; i < 3; i++ {
		_, tail, ok := field(rest)
		if !ok {
			return "", s, false
		}

		rest = tail
	}

	for i := 0; i < 2; i++ {
		tok, tail, ok := field(rest)
		if !ok {
			return "", s, false
		}

		rest = tail

		name, cut := strings.CutSuffix(tok, ":")
		if !cut {
			continue
		}

		if at := strings.IndexByte(name, '['); at >= 0 {
			name = name[:at]
		}

		if name != "" {
			return name, rest, true
		}
	}

	return "", s, false
}

func field(s string) (string, string, bool) {
	s = strings.TrimLeft(s, " ")
	if s == "" {
		return "", "", false
	}

	at := strings.IndexByte(s, ' ')
	if at < 0 {
		return s, "", true
	}

	return s[:at], strings.TrimLeft(s[at+1:], " "), true
}
