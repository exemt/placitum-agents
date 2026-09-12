/*
 * Присутствие на WAF_STATUS — та же шина, что у агента ноды и логгера.
 * Кадр kind=service name=haproxy: балансировщик — служба контура, не нода
 * nginx и не инспектор. Контроллер слушает WAF_STATUS.> и ставит degraded
 * по тишине. Шапка кадра общая (pulse.Frame из placitum-shared).
 *
 * Сверх обычного сервисного кадра едет секция `conf` — какое поколение
 * своего канала (KV policy/haproxy-conf) нода применила: ревизия, хеш и
 * исход. По ней панель считает сходимость канала haproxy.
 */

package pulse

import (
	"github.com/nats-io/nats.go"

	shared "github.com/exemt/placitum-shared/pulse"
)

const Name = "haproxy"

// Conf — применённое поколение: форма та же, что `agent_conf` у ноды nginx.
type Conf struct {
	Rev    int    `json:"rev"`
	SHA256 string `json:"sha256"`
	Apply  string `json:"apply"`
}

type Message struct {
	shared.Frame
	Conf *Conf `json:"conf,omitempty"`
}

func Subject(id string) string {
	return shared.ServiceSubject(Name, id)
}

func Build(id string, ready bool, conf *Conf) Message {
	return Message{
		Frame: shared.NewFrame("service", id, Name, ready, nil),
		Conf:  conf,
	}
}

func Publish(nc *nats.Conn, msg Message) error {
	return shared.PublishFrame(nc, Subject(msg.ID), msg)
}
