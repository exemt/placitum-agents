/*
 * Агент haproxy. Две функции: watch KV policy/haproxy-conf -> `haproxy -c`
 * -> подмена файла -> SIGUSR2 мастеру, и пульс присутствия на WAF_STATUS
 * (kind=service name=haproxy) с секцией conf — применённым поколением.
 *
 * Бутстрап — только окружение: адрес шины, каталог данных, пути haproxy.
 * Ими агент дотягивается до шины, и доставить их шиной нельзя по кругу
 * зависимостей. Всё остальное присылает контроллер.
 */

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
	"github.com/exemt/placitum-agents/haproxy/internal/logkit"
	"github.com/exemt/placitum-agents/haproxy/internal/pulse"
	"github.com/exemt/placitum-agents/haproxy/internal/syslogin"
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
		Bin:       env("WAF_HAPROXY_BIN", "haproxy"),
		CfgPath:   env("WAF_HAPROXY_CFG", "/usr/local/etc/haproxy/haproxy.cfg"),
		PidFile:   env("WAF_HAPROXY_PIDFILE", "/var/run/waf/haproxy.pid"),
		MasterPid: intEnv("WAF_HAPROXY_MASTER_PID", 0),
	}

	level, err := logkit.Env("WAF_HAPROXY_AGENT_LOG", "info")
	if err != nil {
		return err
	}

	/*
	 * Журнал агента -- в waf.log (internal/logkit). Через его же приёмник
	 * ниже едет и журнал самого haproxy: пачка одна, сервисы у строк разные.
	 */
	journal := logkit.Open(logkit.Options{Service: "haproxy-agent", Level: level})
	defer journal.Close()

	log := journal.Log
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

	// После шины и раньше её закрытия: накопленное добивается, пока она жива.
	journal.Attach(ctx, nc)
	defer journal.Close()

	/*
	 * Журнал самого haproxy. Он пишет syslog-датаграммы в сокет рядом
	 * (`log /var/run/waf/log.sock` в конфиге контроллера), агент кладёт их в
	 * ту же пачку, что и свой журнал, -- как агент ноды строки nginx. Сервис у
	 * строк -- haproxy, не haproxy-agent: это журнал балансировщика.
	 */
	stopLog := serveLogSock(journal, log)
	defer stopLog()

	applied, err := desired.Watch(ctx, nc, func(conf *desired.Conf) error {
		return apply.Apply(ctx, applyCfg, conf.Cfg)
	}, log)
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
	// ready — жив ли мастер haproxy, а не сам агент: пульс без этого различия
	// показывал бы зелёную ноду с мёртвым балансировщиком.
	ready := apply.Alive(applyCfg)

	var conf *pulse.Conf
	rev, hash, status := applied.Snapshot()
	if rev > 0 {
		conf = &pulse.Conf{Rev: rev, SHA256: hash, Apply: status}
	}

	msg := pulse.Build(agentID, ready, conf)
	if err := pulse.Publish(nc, msg); err != nil {
		log.Warn("heartbeat failed", "error", err)
		return
	}
	// Кадр раз в четыре секунды -- ход работы, а не событие: debug.
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

/*
 * serveLogSock поднимает приёмник syslog haproxy. Отказ сокета агента не
 * валит: балансировщик по-прежнему пишет в stdout, а раскатка конфига и пульс
 * важнее журнала. WAF_LOG_SOCK= (пусто) выключает приёмник, как у агента
 * ноды; WAF_LOG_SHIP=off -- тоже: класть строки некуда.
 */
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

// envKeep -- как env, но пустое значение -- это ответ, а не его отсутствие.
func envKeep(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
