# Агент Redis

Sidecar рядом с Redis. Пишет присутствие в `WAF_STATUS`: жив ли store, сколько
памяти занято, сколько ключей, плюс CPU/RAM самого контейнера агента.

По агенту на экземпляр. На стенде их два: `redis-agent` при боевом обменнике
(`WAF_REDIS_NAME=redis`) и `redis-internal-agent` при внутреннем Redis контура
(`WAF_REDIS_NAME=redis-internal`), у каждого свой том под `agent.id`. Страница
«Состояние» различает карточки по имени из кадра.

Не нода и не агент nginx: своего `WAF_NODE_ID` нет. Кадр `kind=redis`, тема

```
WAF_STATUS.store.redis.<id>
```

`<id>` — стабильный uuid файла в каталоге данных, не имя сервиса.

## Кадр

```json
{
  "v": 1,
  "kind": "redis",
  "id": "…",
  "name": "redis",
  "hostname": "redis",
  "ready": true,
  "at": "…",
  "host": { "cpu": {}, "memory": {}, "uptime_s": 0 },
  "redis": {
    "ok": true,
    "version": "7.4.2",
    "role": "master",
    "used_memory": 1048576,
    "used_memory_rss": 2097152,
    "maxmemory": 268435456,
    "maxmemory_policy": "noeviction",
    "keys": 12,
    "expires": 3,
    "evicted": 0,
    "hits": 10,
    "misses": 2,
    "clients": 4,
    "uptime_s": 3600
  }
}
```

`ready` / `redis.ok` — ответ на `PING`. Если Redis недоступен, кадр всё равно
уходит: контроллер видит деградацию, а не пропажу агента сразу.

`host` — cgroup агента (gopsutil), не процесс `redis-server`. Размер хранилища —
`redis.used_memory` и `redis.maxmemory` из `INFO`.

## Окружение

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `WAF_NATS_URL` | `nats://127.0.0.1:4222` | шина |
| `REDIS_URL` | `redis://127.0.0.1:6379` | куда ходить `PING` / `INFO` |
| `WAF_REDIS_NAME` | `redis` | имя в кадре, не ключ записи |
| `WAF_DATA_DIR` | `/var/lib/waf/agent` | файл `agent.id` |
| `WAF_HEARTBEAT_EVERY` | `4s` | период |

В deploy/ (`deploy`) — соседний сервис `redis-agent`.
