package pulse

import (
	"github.com/nats-io/nats.go"

	shared "github.com/exemt/placitum-shared/pulse"
)

const Name = "haproxy"

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
