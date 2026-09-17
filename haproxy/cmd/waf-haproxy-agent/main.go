package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/exemt/placitum-agents/haproxy/internal/apply"
	"github.com/exemt/placitum-agents/haproxy/internal/desired"
	"github.com/exemt/placitum-agents/haproxy/internal/id"
	"github.com/exemt/placitum-agents/haproxy/internal/pulse"
	"github.com/exemt/placitum-agents/haproxy/internal/syslogin"
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
	dataDir := env("WAF_DATA_DIR", "/var/lib/waf/agent")
	every := durationEnv("WAF_HEARTBEAT_EVERY", 4*time.Second)

	applyCfg := apply.Config{
		Bin:        env("WAF_HAPROXY_BIN", "haproxy"),
		CfgPath:    env("WAF_HAPROXY_CFG", "/usr/local/etc/haproxy/haproxy.cfg"),
		PidFile:    env("WAF_HAPROXY_PIDFILE", "/var/run/waf/haproxy.pid"),
		MasterPid:  intEnv("WAF_HAPROXY_MASTER_PID", 0),
		MasterSock: env("WAF_HAPROXY_MASTER_SOCK", "/var/run/waf/master.sock"),
	}

	level, err := loglevel.Env("WAF_HAPROXY_AGENT_LOG", "info")
	if err != nil {
		return err
	}

	journal := logkit.Open(logkit.Options{Service: "haproxy-agent", Level: level})
	defer journal.Close()

	log := journal.Log

	log.Info("build", "version", version, "revision", revision)
	slog.SetDefault(log)

	agentID, err := id.Load(dataDir)
	if err != nil {
		return fmt.Errorf("agent id: %w", err)
	}

	nc, err := nats.Connect(natsURL,
		nats.Name("waf-haproxy-agent"),
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	journal.Attach(ctx, nc)
	defer journal.Close()

	stopLog := serveLogSock(journal, log)
	defer stopLog()

	applied, err := desired.Watch(ctx, nc,
		func(conf *desired.Conf) error {
			return apply.Apply(ctx, applyCfg, conf.Cfg)
		},
		func(conf *desired.Conf) bool {
			return apply.Running(ctx, applyCfg, conf.Cfg)
		},
		log,
	)
	if err != nil {
		return fmt.Errorf("desired watch: %w", err)
	}

	subject := pulse.Subject(agentID)
	log.Info("heartbeat on",
		"id", agentID,
		"subject", subject,
		"cfg", applyCfg.CfgPath,
		"pidfile", applyCfg.PidFile,
		"master_pid", applyCfg.MasterPid,
		"master_sock", applyCfg.MasterSock,
		"kv_key", desired.ConfKey,
		"every", every.String(),
	)

	tick := time.NewTicker(every)
	defer tick.Stop()

	beat(nc, agentID, applyCfg, applied, log)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-stop:
			log.Info("shutting down")
			return nil
		case <-tick.C:
			beat(nc, agentID, applyCfg, applied, log)
		}
	}
}

func beat(
	nc *nats.Conn,
	agentID string,
	applyCfg apply.Config,
	applied *desired.Applied,
	log *slog.Logger,
) {
	ready := apply.Alive(applyCfg)

	var conf *pulse.Conf
	rev, hash, status := applied.Snapshot()
	if rev > 0 {
		conf = &pulse.Conf{Rev: rev, SHA256: hash, Apply: status}
	}

	msg := pulse.Build(agentID, ready, conf)
	msg.Version, msg.Revision = version, revision
	if err := pulse.Publish(nc, msg); err != nil {
		log.Warn("heartbeat failed", "error", err)
		return
	}

	alive()

	log.Debug("heartbeat",
		"hostname", msg.Hostname,
		"ready", ready,
		"conf_rev", rev,
		"conf_apply", status,
		"cpu", msg.Host.CPU.Usage,
		"mem_used", msg.Host.Memory.Used,
	)
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

func intEnv(key string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return v
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func serveLogSock(journal *logkit.Journal, log *slog.Logger) func() {
	path := envKeep("WAF_LOG_SOCK", syslogin.DefaultPath)
	if path == "" || journal.Sink() == nil {
		log.Info("log socket off")
		return func() {}
	}

	stop, err := syslogin.Serve(path, journal.Sink(), "haproxy")
	if err != nil {
		log.Error("log socket failed", "path", path, "error", err.Error())
		return func() {}
	}

	log.Info("log socket", "path", path)

	return func() { _ = stop() }
}

func envKeep(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
