/*
 * Агент Redis. Пока одна функция: пульс присутствия на WAF_STATUS.
 * Не нода: своего WAF_NODE_ID нет, кадр kind=redis.
 */

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	"github.com/exemt/placitum-agents/redis/internal/flow"
	"github.com/exemt/placitum-agents/redis/internal/id"
	"github.com/exemt/placitum-agents/redis/internal/logkit"
	"github.com/exemt/placitum-agents/redis/internal/pulse"
	"github.com/exemt/placitum-agents/redis/internal/redisinfo"
)

func main() {
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	natsURL := env("WAF_NATS_URL", "nats://127.0.0.1:4222")
	redisURL := env("REDIS_URL", "redis://127.0.0.1:6379")
	name := env("WAF_REDIS_NAME", "redis")
	dataDir := env("WAF_DATA_DIR", "/var/lib/waf/agent")
	every := durationEnv("WAF_HEARTBEAT_EVERY", 4*time.Second)

	level, err := logkit.Env("WAF_REDIS_AGENT_LOG", "info")
	if err != nil {
		return err
	}

	/*
	 * Журнал агента -- в waf.log (internal/logkit), его канал log -- в пульсе
	 * рядом с cmd. Сервис один на оба экземпляра: различает их writer, то есть
	 * имя машины, -- то же, что hostname кадра.
	 */
	logIO := flow.New()
	journal := logkit.Open(logkit.Options{Service: "redis-agent", Level: level, IO: logIO})
	defer journal.Close()

	log := journal.Log
	slog.SetDefault(log)

	agentID, err := id.Load(dataDir)
	if err != nil {
		return fmt.Errorf("agent id: %w", err)
	}

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(opt)
	defer rdb.Close()

	nc, err := nats.Connect(natsURL,
		nats.Name("waf-redis-agent-"+name),
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

	subject := pulse.Subject(agentID)
	log.Info("heartbeat on",
		"name", name,
		"id", agentID,
		"subject", subject,
		"redis", redisURL,
		"every", every.String(),
	)

	tick := time.NewTicker(every)
	defer tick.Stop()

	rate := &redisinfo.Rate{}

	if err := beat(nc, rdb, agentID, name, rate, logIO, log); err != nil {
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
			if err := beat(nc, rdb, agentID, name, rate, logIO, log); err != nil {
				log.Warn("heartbeat failed", "error", err)
			}
		}
	}
}

func beat(nc *nats.Conn, rdb *redis.Client, agentID, name string, rate *redisinfo.Rate, logIO *flow.Counter, log *slog.Logger) error {
	msg := pulse.Build(context.Background(), agentID, name, rdb, rate)

	// Канал журнала рядом с командами: потери доставки waf.log -- ошибки этой
	// строки кадра, а не строки в самом журнале.
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
		"keys", msg.Redis.Keys,
		"used_memory", msg.Redis.UsedMemory,
		"maxmemory", msg.Redis.MaxMemory,
		"cpu", msg.Host.CPU.Usage,
		"mem_used", msg.Host.Memory.Used,
	)
	return nil
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
