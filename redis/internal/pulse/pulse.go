package pulse

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	"github.com/exemt/placitum-agents/redis/internal/redisinfo"
	"github.com/exemt/placitum-shared/flow"
	shared "github.com/exemt/placitum-shared/pulse"
)

type Message struct {
	shared.Frame
	Redis redisinfo.Snapshot `json:"redis"`
}

func Subject(id string) string {
	return shared.StoreSubject("redis", id)
}

func Build(ctx context.Context, agentID, name string, rdb *redis.Client, rate *redisinfo.Rate) Message {
	store := redisinfo.Collect(ctx, rdb)
	now := time.Now()
	msg := Message{
		Frame: shared.NewFrame("redis", agentID, name, store.OK, nil),
		Redis: store,
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
	return shared.PublishFrame(nc, Subject(msg.ID), msg)
}
