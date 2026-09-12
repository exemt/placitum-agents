/*
 * Присутствие на WAF_STATUS — та же шина, что у агента ноды и логгера.
 * Кадр kind=service name=haproxy: балансировщик — служба контура, не нода
 * nginx и не инспектор. Контроллер слушает WAF_STATUS.> и ставит degraded
 * по тишине.
 *
 * Сверх обычного сервисного кадра едет секция `conf` — какое поколение
 * своего канала (KV policy/haproxy-conf) нода применила: ревизия, хеш и
 * исход. По ней панель считает сходимость канала haproxy.
 */

package pulse

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/exemt/placitum-agents/haproxy/internal/host"
)

const Name = "haproxy"

// Conf — применённое поколение: форма та же, что `agent_conf` у ноды nginx.
type Conf struct {
	Rev    int    `json:"rev"`
	SHA256 string `json:"sha256"`
	Apply  string `json:"apply"`
}

type Message struct {
	V        int           `json:"v"`
	Kind     string        `json:"kind"`
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Hostname string        `json:"hostname"`
	Ready    bool          `json:"ready"`
	At       string        `json:"at"`
	Host     host.Snapshot `json:"host"`
	Conf     *Conf         `json:"conf,omitempty"`
}

func Subject(id string) string {
	return fmt.Sprintf("WAF_STATUS.service.%s.%s", Name, token(id))
}

func Build(id string, ready bool, conf *Conf) Message {
	snap := host.Collect()
	return Message{
		V:        1,
		Kind:     "service",
		ID:       id,
		Name:     Name,
		Hostname: snap.Hostname,
		Ready:    ready,
		At:       time.Now().UTC().Format(time.RFC3339Nano),
		Host:     snap,
		Conf:     conf,
	}
}

func Publish(nc *nats.Conn, msg Message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return nc.Publish(Subject(msg.ID), body)
}

func token(s string) string {
	r := strings.NewReplacer(".", "_", ">", "_", "*", "_", " ", "_")
	return r.Replace(s)
}
