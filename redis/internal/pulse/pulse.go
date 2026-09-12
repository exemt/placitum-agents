package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	"github.com/exemt/placitum-agents/redis/internal/flow"
	"github.com/exemt/placitum-agents/redis/internal/host"
	"github.com/exemt/placitum-agents/redis/internal/redisinfo"
)

// Message — кадр присутствия Redis. Не нода: своего node_id нет.
type Message struct {
	V        int                  `json:"v"`
	Kind     string               `json:"kind"`
	ID       string               `json:"id"`
	Name     string               `json:"name"`
	Hostname string               `json:"hostname"`
	Ready    bool                 `json:"ready"`
	At       string               `json:"at"`
	Host     host.Snapshot        `json:"host"`
	Redis    redisinfo.Snapshot   `json:"redis"`
	WindowS  int                  `json:"window_s,omitempty"`
	IO       map[string]flow.Flow `json:"io,omitempty"`
}

func Subject(id string) string {
	return fmt.Sprintf("WAF_STATUS.store.redis.%s", id)
}

func Build(ctx context.Context, agentID, name string, rdb *redis.Client, rate *redisinfo.Rate) Message {
	snap := host.Collect()
	store := redisinfo.Collect(ctx, rdb)
	now := time.Now()
	msg := Message{
		V:        1,
		Kind:     "redis",
		ID:       agentID,
		Name:     name,
		Hostname: snap.Hostname,
		Ready:    store.OK,
		At:       now.UTC().Format(time.RFC3339Nano),
		Host:     snap,
		Redis:    store,
	}
	if store.OK {
		cmd, windowS := rate.Sample(store, now)
		if windowS > 0 {
			msg.WindowS = windowS
			msg.IO = map[string]flow.Flow{"cmd": cmd}
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
