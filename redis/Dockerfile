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
RUN go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" -o /out/waf-redis-agent ./cmd/waf-redis-agent

FROM alpine:3.22

ARG VERSION=dev
ARG REVISION=unknown

LABEL org.opencontainers.image.title="placitum/agents-redis" \
      org.opencontainers.image.description="Placitum Redis agent: health, memory and pulse of a Redis instance" \
      org.opencontainers.image.source="https://github.com/exemt/placitum-agents" \
      org.opencontainers.image.licenses="LicenseRef-Placitum" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"

COPY --from=build /out/waf-redis-agent /usr/local/bin/waf-redis-agent

RUN adduser -D -H -u 10003 wafagent \
    && mkdir -p /var/lib/waf/agent \
    && chown wafagent:wafagent /var/lib/waf/agent

USER wafagent

ENV WAF_NATS_URL=nats://nats:4222 \
    REDIS_URL=redis://redis:6379 \
    WAF_REDIS_NAME=redis \
    WAF_DATA_DIR=/var/lib/waf/agent \
    WAF_HEARTBEAT_EVERY=4s

HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=3 \
    CMD sh -c 'test $(( $(date +%s) - $(stat -c %Y "$WAF_DATA_DIR/alive") )) -lt 20'
CMD ["waf-redis-agent"]
