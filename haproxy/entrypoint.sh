#!/bin/sh
# haproxy starts first on a bootstrap configuration, then the agent: the agent reloads the master
# through its CLI, and there is nothing to reload before the master runs.
set -e

CFG="${WAF_HAPROXY_CFG:-/usr/local/etc/haproxy/haproxy.cfg}"
PIDFILE="${WAF_HAPROXY_PIDFILE:-/var/run/waf/haproxy.pid}"
MASTER_SOCK="${WAF_HAPROXY_MASTER_SOCK:-/var/run/waf/master.sock}"

# The bootstrap configuration lives until the first generation from the controller.
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
    option                  forwardfor
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

# -W: master-worker mode. -S: the master CLI; the agent reloads through it and gets back whether
# the new configuration loaded. A pidfile from the previous start survives a container restart and
# would point the agent at a stranger's pid.
rm -f "$PIDFILE"
haproxy -W -S "$MASTER_SOCK,mode,600" -f "$CFG" -p "$PIDFILE" &

export WAF_HAPROXY_MASTER_PID=$!

exec waf-haproxy-agent "$@"
