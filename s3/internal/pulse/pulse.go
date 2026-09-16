package pulse

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/exemt/placitum-agents/s3/internal/s3info"
	"github.com/exemt/placitum-shared/flow"
	shared "github.com/exemt/placitum-shared/pulse"
)

type Message struct {
	shared.Frame
	S3 s3info.Snapshot `json:"s3"`
}

func Subject(id string) string {
	return shared.StoreSubject("s3", id)
}

func Build(ctx context.Context, agentID, name string, probe s3info.Probe, rate *s3info.Rate) Message {
	store := s3info.Collect(ctx, probe)
	now := time.Now()
	msg := Message{
		Frame: shared.NewFrame("s3", agentID, name, store.OK, nil),
		S3:    store,
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
	return shared.PublishFrame(nc, Subject(msg.ID), msg)
}
