package syslogin

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/exemt/placitum-agents/haproxy/internal/logkit"
)

var at = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

// Строка трафика haproxy: pid в теге не становится частью сервиса, а
// двоеточие в адресе клиента -- тегом.
func TestParseHaproxyLine(t *testing.T) {
	raw := "<134>Sep 11 12:00:00 haproxy[27]: 172.18.0.1:51234 [11/Sep/2026:12:00:00.001] fe_waf be_nginx/edge-01 0/0/1/2/3 200 512 - - ---- 1/1/0/0/0 0/0 \"GET / HTTP/1.1\""

	line, ok := Parse([]byte(raw), at, "haproxy")
	if !ok {
		t.Fatal("строка не разобрана")
	}

	if line.Service != "haproxy" || line.Severity != "info" {
		t.Fatalf("шапка: service=%q severity=%q", line.Service, line.Severity)
	}

	if !strings.HasPrefix(line.Text, "172.18.0.1:51234 [") {
		t.Fatalf("text: %q", line.Text)
	}

	if !line.TS.Equal(at) {
		t.Fatalf("время: %v", line.TS)
	}
}

// С log-send-hostname тег стоит вторым полем.
func TestParseWithHostname(t *testing.T) {
	line, ok := Parse([]byte("<131>Sep  1 01:02:03 lb-1 haproxy[8]: Server be_nginx/edge-02 is DOWN"), at, "x")
	if !ok || line.Service != "haproxy" || line.Severity != "error" {
		t.Fatalf("строка: %+v ok=%v", line, ok)
	}

	if line.Text != "Server be_nginx/edge-02 is DOWN" {
		t.Fatalf("text: %q", line.Text)
	}
}

// Шапка не разобралась -- строка всё равно едет, целиком и с сервисом по
// умолчанию: выбросить её значило бы потерять ровно то, ради чего смотрят.
func TestParseWithoutHeader(t *testing.T) {
	line, ok := Parse([]byte("plain text without header\n"), at, "haproxy")
	if !ok || line.Service != "haproxy" || line.Severity != "" ||
		line.Text != "plain text without header" {
		t.Fatalf("строка: %+v ok=%v", line, ok)
	}

	if _, ok := Parse([]byte("\x00\n"), at, "haproxy"); ok {
		t.Fatal("пустая датаграмма принята")
	}
}

// Сокет живой: датаграмма доходит до пачки с сервисом из тега.
func TestServeFeedsSink(t *testing.T) {
	t.Setenv("WAF_LOG_SHIP", "on")

	sink := logkit.NewSink("lb-1", "haproxy-agent", nil)
	defer sink.Close()

	path := filepath.Join(t.TempDir(), "log.sock")

	stop, err := Serve(path, sink, "haproxy")
	if err != nil {
		t.Skipf("unixgram недоступен: %v", err)
	}
	defer stop()

	conn, err := net.Dial("unixgram", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("<134>Sep 11 12:00:00 haproxy[27]: hello")); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)

	for time.Now().Before(deadline) {
		if sink.Dropped() == 0 && sink.Pending() > 0 {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("строка не дошла до приёмника")
}
