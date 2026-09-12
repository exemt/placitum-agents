/*
 * Агент S3. Пока одна функция: пульс присутствия на WAF_STATUS.
 * Не нода: своего WAF_NODE_ID нет, кадр kind=s3.
 */

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/minio/madmin-go/v3"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/nats-io/nats.go"

	"github.com/exemt/placitum-agents/s3/internal/flow"
	"github.com/exemt/placitum-agents/s3/internal/id"
	"github.com/exemt/placitum-agents/s3/internal/pulse"
	"github.com/exemt/placitum-agents/s3/internal/s3info"
	"github.com/exemt/placitum-shared/logkit"
	"github.com/exemt/placitum-shared/loglevel"
)

func main() {
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	natsURL := env("WAF_NATS_URL", "nats://127.0.0.1:4222")
	endpointRaw := env("S3_ENDPOINT", "http://127.0.0.1:9000")
	access := env("S3_ACCESS_KEY", "waf")
	secret := env("S3_SECRET_KEY", "wafwafwaf")
	bucket := env("S3_BUCKET", "waf-bodies")
	region := env("S3_REGION", "us-east-1")
	name := env("WAF_S3_NAME", "s3")
	dataDir := env("WAF_DATA_DIR", "/var/lib/waf/agent")
	every := durationEnv("WAF_HEARTBEAT_EVERY", 4*time.Second)

	level, err := loglevel.Env("WAF_S3_AGENT_LOG", "info")
	if err != nil {
		return err
	}

	// Журнал агента -- в waf.log (shared/logkit), его канал log -- в пульсе
	// рядом с api.
	logIO := flow.New()
	journal := logkit.Open(logkit.Options{Service: "s3-agent", Level: level, IO: logIO})
	defer journal.Close()

	log := journal.Log
	slog.SetDefault(log)

	agentID, err := id.Load(dataDir)
	if err != nil {
		return fmt.Errorf("agent id: %w", err)
	}

	ep, err := s3info.ParseEndpoint(endpointRaw)
	if err != nil {
		return fmt.Errorf("S3_ENDPOINT: %w", err)
	}

	s3c, err := minio.New(ep.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(access, secret, ""),
		Secure: ep.Secure,
		Region: region,
	})
	if err != nil {
		return fmt.Errorf("s3 client: %w", err)
	}

	admin, err := madmin.New(ep.Host, access, secret, ep.Secure)
	if err != nil {
		log.Warn("minio admin unavailable", "error", err)
		admin = nil
	}

	for _, name := range ensureBuckets(bucket) {
		if err := ensureBucket(s3c, name, region, log); err != nil {
			log.Warn("ensure bucket failed", "bucket", name, "error", err)
		}
	}

	nc, err := nats.Connect(natsURL,
		nats.Name("waf-s3-agent-"+name),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(500*time.Millisecond),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			log.Warn("bus disconnected", "error", errText(err))
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			log.Info("bus reconnected")
		}),
	)
	if err != nil {
		return fmt.Errorf("nats: %w", err)
	}
	defer nc.Close()

	// После шины и раньше её закрытия: накопленное добивается, пока она жива.
	journal.Attach(context.Background(), nc)
	defer journal.Close()

	probe := s3info.Probe{
		S3:       s3c,
		Admin:    admin,
		Endpoint: ep.Host,
		Secure:   ep.Secure,
		Access:   access,
		Secret:   secret,
		Bucket:   bucket,
		Region:   region,
	}

	subject := pulse.Subject(agentID)
	log.Info("heartbeat on",
		"name", name,
		"id", agentID,
		"subject", subject,
		"s3", endpointRaw,
		"bucket", bucket,
		"every", every.String(),
	)

	tick := time.NewTicker(every)
	defer tick.Stop()

	rate := &s3info.Rate{}

	if err := beat(nc, probe, agentID, name, rate, logIO, log); err != nil {
		log.Warn("heartbeat failed", "error", err)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-stop:
			log.Info("shutting down")
			return nil
		case <-tick.C:
			if err := beat(nc, probe, agentID, name, rate, logIO, log); err != nil {
				log.Warn("heartbeat failed", "error", err)
			}
		}
	}
}

func beat(nc *nats.Conn, probe s3info.Probe, agentID, name string, rate *s3info.Rate, logIO *flow.Counter, log *slog.Logger) error {
	msg := pulse.Build(context.Background(), agentID, name, probe, rate)

	// Канал журнала рядом с api: потери доставки waf.log -- ошибки этой строки
	// кадра, а не строки в самом журнале.
	if msg.IO == nil {
		msg.IO = map[string]flow.Flow{}
	}
	msg.IO["log"] = logIO.Snapshot()

	if err := pulse.Publish(nc, msg); err != nil {
		return err
	}

	// Кадр раз в четыре секунды -- ход работы, а не событие: debug.
	log.Debug("heartbeat",
		"hostname", msg.Hostname,
		"ready", msg.Ready,
		"objects", msg.S3.Objects,
		"used_bytes", msg.S3.UsedBytes,
		"capacity", msg.S3.Capacity,
		"cpu", msg.Host.CPU.Usage,
		"mem_used", msg.Host.Memory.Used,
	)
	return nil
}

func ensureBucket(c *minio.Client, bucket, region string, log *slog.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ok, err := c.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	if err := c.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: region}); err != nil {
		return err
	}
	log.Info("bucket created", "bucket", bucket)
	return nil
}

func ensureBuckets(primary string) []string {
	raw := env("S3_ENSURE_BUCKETS", primary)
	seen := map[string]bool{}
	out := []string{}

	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}

	if !seen[primary] {
		out = append([]string{primary}, out...)
	}

	return out
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
