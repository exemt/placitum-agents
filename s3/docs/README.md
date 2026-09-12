# Агент S3

Sidecar рядом с S3-совместимым обменником (в стенде — MinIO). Пишет присутствие в
`WAF_STATUS`: жив ли store, сколько объектов и байт занято, плюс CPU/RAM самого
контейнера агента.

Не нода и не агент nginx: своего `WAF_NODE_ID` нет. Кадр `kind=s3`, тема

```
WAF_STATUS.store.s3.<id>
```

`<id>` — стабильный uuid файла в каталоге данных, не имя сервиса.

## Кадр

```json
{
  "v": 1,
  "kind": "s3",
  "id": "…",
  "name": "s3",
  "hostname": "s3-agent",
  "ready": true,
  "at": "…",
  "host": { "cpu": {}, "memory": {}, "uptime_s": 0 },
  "s3": {
    "ok": true,
    "version": "2025-04-22T22-12-26Z",
    "endpoint": "minio:9000",
    "bucket": "waf-bodies",
    "region": "us-east-1",
    "objects": 12,
    "used_bytes": 1048576,
    "capacity": 10737418240,
    "buckets": 1,
    "uptime_s": 3600
  }
}
```

`ready` / `s3.ok` — бакет существует и отвечает. Если обменник недоступен, кадр
всё равно уходит: контроллер видит, что агент жив, а store — нет.

`host` — cgroup агента (gopsutil), не процесс MinIO. Объём обменника — admin API
MinIO (`ServerInfo` / `DataUsageInfo`). Темп в `io.api` — дельта prometheus-
счётчиков MinIO (`/minio/metrics/v3/api/requests`, запасной путь —
`/minio/v2/metrics/cluster`): запросы, байты, ошибки. На обычном S3 без
admin/metrics остаётся проверка бакета, без `io`.

При старте агент создаёт бакет, если его ещё нет.

## Окружение

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `WAF_NATS_URL` | `nats://127.0.0.1:4222` | шина |
| `S3_ENDPOINT` | `http://127.0.0.1:9000` | API обменника |
| `S3_ACCESS_KEY` | `waf` | ключ |
| `S3_SECRET_KEY` | `wafwafwaf` | секрет |
| `S3_BUCKET` | `waf-bodies` | какой бакет щупать в пульсе |
| `S3_ENSURE_BUCKETS` | значение `S3_BUCKET` | через запятую: бакеты, которые создать при старте (`waf-bodies,waf-headers,waf-args`) |
| `S3_REGION` | `us-east-1` | регион подписи |
| `WAF_S3_NAME` | `s3` | имя в кадре, не ключ записи |
| `WAF_DATA_DIR` | `/var/lib/waf/agent` | файл `agent.id` |
| `WAF_HEARTBEAT_EVERY` | `4s` | период |

В deploy/ (`deploy`) — сервисы `minio` и `s3-agent`.
