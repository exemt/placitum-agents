# Контекст -- agents/s3, модуль Go самостоятелен.
#
#     docker build -t placitum/agents-s3 .

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

# Сборка в бинарь: версия и ревизия видны в журнале старта и в кадре
# присутствия, а не только в метках образа.
ARG VERSION=dev
ARG REVISION=unknown
RUN go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" -o /out/waf-s3-agent ./cmd/waf-s3-agent

FROM alpine:3.22

# Версия и ревизия приходят снаружи: .git в контекст сборки не попадает.
ARG VERSION=dev
ARG REVISION=unknown

LABEL org.opencontainers.image.title="placitum/agents-s3" \
      org.opencontainers.image.description="Placitum S3 agent: bucket health and pulse of the archive store" \
      org.opencontainers.image.source="https://github.com/exemt/placitum-agents" \
      org.opencontainers.image.licenses="LicenseRef-Placitum" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"


COPY --from=build /out/waf-s3-agent /usr/local/bin/waf-s3-agent

RUN adduser -D -H -u 10004 wafagent \
    && mkdir -p /var/lib/waf/agent \
    && chown wafagent:wafagent /var/lib/waf/agent

USER wafagent

ENV WAF_NATS_URL=nats://nats:4222 \
    S3_ENDPOINT=http://minio:9000 \
    S3_ACCESS_KEY=waf \
    S3_SECRET_KEY=wafwafwaf \
    S3_BUCKET=waf-bodies \
    S3_REGION=us-east-1 \
    WAF_S3_NAME=s3 \
    WAF_DATA_DIR=/var/lib/waf/agent \
    WAF_HEARTBEAT_EVERY=4s

# Жив -- значит шлёт кадры: агент обновляет отметку после каждого кадра
# присутствия (раз в WAF_HEARTBEAT_EVERY), проверка смотрит на её возраст.
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=3 \
    CMD sh -c 'test $(( $(date +%s) - $(stat -c %Y "$WAF_DATA_DIR/alive") )) -lt 20'
CMD ["waf-s3-agent"]
