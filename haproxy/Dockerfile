FROM golang:1.25-alpine AS build

WORKDIR /src

ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ENV CGO_ENABLED=0

ARG VERSION=dev
ARG REVISION=unknown
RUN go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" -o /out/waf-haproxy-agent ./cmd/waf-haproxy-agent

FROM haproxy:3.0-alpine

ARG VERSION=dev
ARG REVISION=unknown

LABEL org.opencontainers.image.title="placitum/agents-haproxy" \
      org.opencontainers.image.description="Placitum HAProxy node: balancer with controller-managed configuration" \
      org.opencontainers.image.source="https://github.com/exemt/placitum-agents" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"

USER root

COPY --from=build /out/waf-haproxy-agent /usr/local/bin/waf-haproxy-agent
COPY entrypoint.sh /usr/local/bin/waf-haproxy-entrypoint.sh

RUN chmod +x /usr/local/bin/waf-haproxy-entrypoint.sh \
    && mkdir -p /var/run/waf /var/lib/waf/agent \
    && chown -R haproxy:haproxy /var/run/waf /var/lib/waf /usr/local/etc/haproxy

USER haproxy

ENV WAF_NATS_URL=nats://nats:4222 \
    WAF_DATA_DIR=/var/lib/waf/agent \
    WAF_HAPROXY_BIN=haproxy \
    WAF_HAPROXY_CFG=/usr/local/etc/haproxy/haproxy.cfg \
    WAF_HAPROXY_PIDFILE=/var/run/waf/haproxy.pid \
    WAF_HAPROXY_MASTER_SOCK=/var/run/waf/master.sock \
    WAF_HEARTBEAT_EVERY=4s

HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=3 \
    CMD sh -c 'test $(( $(date +%s) - $(stat -c %Y "$WAF_DATA_DIR/alive") )) -lt 20'
ENTRYPOINT ["/usr/local/bin/waf-haproxy-entrypoint.sh"]
