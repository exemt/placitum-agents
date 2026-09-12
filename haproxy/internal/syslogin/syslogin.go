/*
 * Приёмник syslog рядом с процессом: haproxy пишет свой журнал датаграммами в
 * unix-сокет, агент кладёт строки в ту же пачку waf.log, что и свой журнал.
 *
 * Та же дорога, что у агента ноды со строками nginx
 * (nginx/agent/internal/nginxlog): писать в syslog haproxy умеет сам, своего
 * клиента шины у него нет, а агент рядом есть по построению -- он раскатывает
 * конфиг. Копия, не перенос: `log stdout` в конфиге остаётся, и
 * `docker compose logs haproxy` по-прежнему первое, куда смотрят.
 *
 * Отличие от разбора nginx одно -- тег. haproxy ставит в него pid
 * (`haproxy[27]:`), и сервис берётся без него: иначе каждый reload мастера
 * давал бы в таблице новый сервис, а фильтр по нему -- пустое окно.
 *
 * Промах сокета -- потеря строки лога, не запроса: на unixgram haproxy не
 * блокируется. Сокета ещё нет (агент стартует после мастера) -- строки старта
 * остаются только в stdout.
 */

package syslogin

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/exemt/placitum-agents/haproxy/internal/logkit"
)

const (
	// DefaultPath -- тот же путь, что у сокета логов агента ноды: адрес
	// приёмника на любой машине контура один.
	DefaultPath = "/var/run/waf/log.sock"

	// MaxBytes -- потолок датаграммы. В конфиге контроллера `len 8192`,
	// вдвое -- запас на чужого писателя в тот же сокет.
	MaxBytes = 16 << 10

	// Очередь приёма: строки короткие, четыре мегабайта -- десятки тысяч
	// непрочитанных, столько копится разве что за паузу GC.
	readBuffer = 4 << 20

	// maxText -- потолок текста строки, как у агента ноды и у логгера.
	maxText = 8 << 10
)

// severities -- имена уровней syslog по индексу, словами фильтра waf.log.
var severities = [8]string{
	"emerg", "alert", "crit", "error", "warn", "notice", "info", "debug",
}

/*
 * Serve слушает path и кладёт каждую разобранную датаграмму в sink. Сокет
 * 0666: haproxy и агент в одном контейнере, но пользователь у мастера свой.
 * Остановка закрывает сокет и снимает файл.
 */
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

	// Меньшая очередь полезнее отсутствия приёмника: ядро могло срезать
	// размер до net.core.rmem_max.
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

/*
 * Parse разбирает датаграмму RFC 3164:
 *
 *     <PRI>Mmm dd hh:mm:ss [hostname] tag[pid]: текст
 *
 * Не разобралось -- не ошибка: строка едет целиком в text, без уровня и с
 * сервисом по умолчанию. Время -- момент приёма: в шапке RFC 3164 нет ни года,
 * ни зоны, а собственная отметка haproxy остаётся внутри текста.
 */
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

// priority снимает "<134>" и отдаёт значение PRI.
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

/*
 * head снимает "Mmm dd hh:mm:ss [hostname] tag[pid]:" и отдаёт тег без pid.
 * Поле с двоеточием ищется не дальше двух шагов от отметки времени: дальше
 * начинается текст, в котором двоеточий сколько угодно (адрес:порт клиента).
 */
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

// field снимает одно поле, разделённое пробелами, и отдаёт остаток уже без
// ведущих пробелов.
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
