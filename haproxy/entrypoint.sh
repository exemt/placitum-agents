#!/bin/sh
# Порядок обратный обычному: сначала поднимается haproxy на bootstrap-конфиге,
# и только потом агент. Агент применяет поколение через SIGUSR2 мастеру, а
# перечитывать нечего, пока мастер не запущен.
set -e

CFG="${WAF_HAPROXY_CFG:-/usr/local/etc/haproxy/haproxy.cfg}"
PIDFILE="${WAF_HAPROXY_PIDFILE:-/var/run/waf/haproxy.pid}"

# Bootstrap живёт ровно до первого поколения: watch отдаёт текущее значение
# KV сразу после старта, и контроллер перезапишет файл. Содержимое повторяет
# исторический deploy/haproxy/haproxy.cfg — до первой рассылки нода ведёт
# себя ровно как статичный стенд.
if [ ! -f "$CFG" ]; then
    cat > "$CFG" <<'EOF'
global
    maxconn     4096
    log         stdout format raw local0
    log         /var/run/waf/log.sock len 8192 local0
    tune.bufsize 1048576

resolvers docker
    nameserver dns 127.0.0.11:53
    resolve_retries 3
    timeout resolve 1s
    timeout retry   1s
    hold valid      10s

defaults
    mode                    http
    log                     global
    option                  httplog
    option                  dontlognull
    option                  http-keep-alive
    timeout connect         2s
    timeout client          30s
    timeout server          30s
    timeout http-keep-alive 5s
    timeout tunnel          1h

frontend fe_waf
    bind *:8080
    default_backend be_nginx

backend be_nginx
    balance roundrobin
    option httpchk GET /healthz
    http-check expect status 200
    server edge-01 edge-01:8080 check inter 2s resolvers docker init-addr last,libc,none
    server edge-02 edge-02:8080 check inter 2s resolvers docker init-addr last,libc,none
    server edge-03 edge-03:8080 check inter 2s resolvers docker init-addr last,libc,none

listen stats
    bind *:8404
    stats enable
    stats uri /
    stats refresh 5s
EOF
fi

# -W: master-worker, только он умеет reload по SIGUSR2. Мастер в фоне, его
# pid в файле — агенту для сигнала и для ready в пульсе.
#
# pidfile прошлого запуска убирается до старта: он лежит в слое контейнера и
# переживает `docker compose restart`, а pid мастера на новом старте другой
# (на стенде 8 -> 7). Старый номер — сигнал чужому процессу и ложный ready в
# пульсе; пока нового файла нет, агент видит «мастер ещё грузится».
rm -f "$PIDFILE"
haproxy -W -f "$CFG" -p "$PIDFILE" &

# pid мастера из первых рук: pidfile с другим номером агент считает чужим.
export WAF_HAPROXY_MASTER_PID=$!

exec waf-haproxy-agent "$@"
