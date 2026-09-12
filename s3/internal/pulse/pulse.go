package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/exemt/placitum-agents/s3/internal/flow"
	"github.com/exemt/placitum-agents/s3/internal/host"
	"github.com/exemt/placitum-agents/s3/internal/s3info"
)

// Message — кадр присутствия S3. Не нода: своего node_id нет.
type Message struct {
	V        int                  `json:"v"`
	Kind     string               `json:"kind"`
	ID       string               `json:"id"`
	Name     string               `json:"name"`
	Hostname string               `json:"hostname"`
	Ready    bool                 `json:"ready"`
	At       string               `json:"at"`
	Host     host.Snapshot        `json:"host"`
	S3       s3info.Snapshot      `json:"s3"`
	WindowS  int                  `json:"window_s,omitempty"`
	IO       map[string]flow.Flow `json:"io,omitempty"`
}

func Subject(id string) string {
	return fmt.Sprintf("WAF_STATUS.store.s3.%s", id)
}

func Build(ctx context.Context, agentID, name string, probe s3info.Probe, rate *s3info.Rate) Message {
	snap := host.Collect()
	store := s3info.Collect(ctx, probe)
	now := time.Now()
	msg := Message{
		V:        1,
		Kind:     "s3",
		ID:       agentID,
		Name:     name,
		Hostname: snap.Hostname,
		Ready:    store.OK,
		At:       now.UTC().Format(time.RFC3339Nano),
		Host:     snap,
		S3:       store,
	}
	if store.OK && store.HasAPI && rate != nil {
		api, windowS := rate.Sample(store, now)
		if windowS > 0 {
			msg.WindowS = windowS
			msg.IO = map[string]flow.Flow{"api": api}
		}
	}
	return msg
}

func Publish(nc *nats.Conn, msg Message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return nc.Publish(Subject(msg.ID), body)
}
