# Балансировщик, конфигурацию которого ведёт контроллер: haproxy и агент в
# одном контейнере. Агент применяет поколение через `haproxy -c` и SIGUSR2
# мастеру — валидация требует того же бинаря, что держит трафик, поэтому
# агент живёт не сайдкаром, а на самой ноде (та же логика, что у edge-managed,
# см. docs/config-distribution.md).
#
#     docker compose up -d haproxy

FROM golang:1.25-alpine AS build

WORKDIR /src

# Прокси модулей -- аргументом: в изолированном контуре подставляют своё зеркало.
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

# Зависимости отдельным слоем; download, а не tidy: сборка обязана повторять
# go.mod, а не переписывать его.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ENV CGO_ENABLED=0

# Тесты в сборке: образ с красными тестами ставить нельзя.
RUN go test ./...

RUN go build -trimpath -ldflags="-s -w" -o /out/waf-haproxy-agent ./cmd/waf-haproxy-agent

FROM haproxy:3.0-alpine

# Версия и ревизия приходят снаружи: .git в контекст сборки не попадает.
ARG VERSION=dev
ARG REVISION=unknown

LABEL org.opencontainers.image.title="placitum/agents-haproxy" \
      org.opencontainers.image.description="Placitum HAProxy node: balancer with controller-managed configuration" \
      org.opencontainers.image.source="https://github.com/exemt/placitum-agents" \
      org.opencontainers.image.licenses="LicenseRef-Placitum" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"


USER root

COPY --from=build /out/waf-haproxy-agent /usr/local/bin/waf-haproxy-agent
COPY entrypoint.sh /usr/local/bin/waf-haproxy-entrypoint.sh

# Каталог конфига пишет агент, pidfile — мастер: оба от пользователя haproxy.
RUN chmod +x /usr/local/bin/waf-haproxy-entrypoint.sh \
    && mkdir -p /var/run/waf /var/lib/waf/agent \
    && chown -R haproxy:haproxy /var/run/waf /var/lib/waf /usr/local/etc/haproxy

USER haproxy

ENV WAF_NATS_URL=nats://nats:4222 \
    WAF_DATA_DIR=/var/lib/waf/agent \
    WAF_HAPROXY_BIN=haproxy \
    WAF_HAPROXY_CFG=/usr/local/etc/haproxy/haproxy.cfg \
    WAF_HAPROXY_PIDFILE=/var/run/waf/haproxy.pid \
    WAF_HEARTBEAT_EVERY=4s

ENTRYPOINT ["/usr/local/bin/waf-haproxy-entrypoint.sh"]
