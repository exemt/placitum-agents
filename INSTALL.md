# Installation

English · [Русский](INSTALL.ru.md)

Three processes, three different reasons to install them. Usually `placitum-core` installs the Redis
and S3 agents; the haproxy node is not part of the installation yet.

## redis: instance monitor

One agent per Redis instance. A standard installation has two, the exchange and the internal
instance, and the agents differ only in address and name; the panel status page tells them apart by
name.

| Variable | Default | Purpose |
| --- | --- | --- |
| `WAF_NATS_URL` | `nats://127.0.0.1:4222`; `nats://nats:4222` in the image | bus: presence frame and log |
| `REDIS_URL` | `redis://127.0.0.1:6379`; `redis://redis:6379` in the image | the instance to watch |
| `WAF_REDIS_NAME` | `redis` | name in the frame |
| `WAF_DATA_DIR` | `/var/lib/waf/agent` | agent state: the `agent.id` file |
| `WAF_HEARTBEAT_EVERY` | `4s` | frame interval |
| `WAF_REDIS_AGENT_LOG` | `info` | log level |

```yaml
  redis-agent:
    image: placitum/agents-redis
    environment:
      WAF_NATS_URL: nats://nats:4222
      REDIS_URL: redis://redis:6379
      WAF_REDIS_NAME: redis
  redis-internal-agent:
    image: placitum/agents-redis
    environment:
      WAF_NATS_URL: nats://nats:4222
      REDIS_URL: redis://redis-internal:6379
      WAF_REDIS_NAME: redis-internal
```

The frame carries the `PING` result and `INFO` numbers: version, role, used and maximum memory,
eviction policy, keys, expiring keys, evictions, hits, misses, clients, uptime. If Redis is down the
frame still goes out, so the controller sees a degraded store, not a missing agent.

## s3: archive monitor

One per installation. Besides watching, it does one useful thing: **it creates the archive buckets**
that do not exist yet.

| Variable | Default | Purpose |
| --- | --- | --- |
| `WAF_NATS_URL` | `nats://127.0.0.1:4222`; `nats://nats:4222` in the image | bus |
| `S3_ENDPOINT` | `http://127.0.0.1:9000`; `http://minio:9000` in the image | storage address |
| `S3_ACCESS_KEY`, `S3_SECRET_KEY` | `waf`, `wafwafwaf` | credentials |
| `S3_REGION` | `us-east-1` | signing region |
| `S3_BUCKET` | `waf-bodies` | bucket checked for the frame |
| `S3_ENSURE_BUCKETS` | the value of `S3_BUCKET` | comma-separated buckets to create at start |
| `WAF_S3_NAME` | `s3` | name in the frame |
| `WAF_DATA_DIR` | `/var/lib/waf/agent` | agent state: the `agent.id` file |
| `WAF_HEARTBEAT_EVERY` | `4s` | frame interval |

Credentials come as variables, not a file. This is a deliberate exception: the agent holds no
installation secrets, and a neighbouring process knows the storage keys anyway. Where that is not
acceptable, use the secrets of your orchestration platform.

With MinIO the frame also carries object count, used bytes and request rate from its admin and
metrics APIs; plain S3 without them gets only the bucket check.

## haproxy: balancer node

Not a monitor but a node: haproxy and the agent in one container. The controller publishes a
rendered `haproxy.cfg` in the `policy/haproxy-conf` KV document; the agent checks its hash, runs
`haproxy -c`, swaps the file and sends `SIGUSR2` to the master.

| Variable | Default | Purpose |
| --- | --- | --- |
| `WAF_NATS_URL` | `nats://127.0.0.1:4222`; `nats://nats:4222` in the image | bus: configuration and presence frame |
| `WAF_HAPROXY_BIN` | `haproxy` | binary that checks and runs the configuration |
| `WAF_HAPROXY_CFG` | `/usr/local/etc/haproxy/haproxy.cfg` | where the agent writes the configuration |
| `WAF_HAPROXY_PIDFILE` | `/var/run/waf/haproxy.pid` | master pid for the signal |
| `WAF_DATA_DIR` | `/var/lib/waf/agent` | state: the applied revision |
| `WAF_HAPROXY_AGENT_LOG` | `info` | log level |

The start order is reversed: haproxy comes up first with a bootstrap configuration, then the agent,
because there is nothing to reload before the master runs. A failed `haproxy -c` leaves the live
file untouched and reports `apply_failed` in the frame.

## Checking

All three are checked the same way, by their presence frames on the bus:

```sh
nats sub 'WAF_STATUS.store.>' --count 2
nats sub 'WAF_STATUS.service.haproxy.>' --count 1
```

The panel status page shows them: Redis by instance name, S3 by bucket, haproxy with the applied
configuration revision.

## Pitfalls

- **Two Redis agents need two names.** With the same `WAF_REDIS_NAME` both show up as one card, and
  you cannot tell which instance ran out of memory.
- **List every archive bucket in `S3_ENSURE_BUCKETS`.** By default only `S3_BUCKET` is created, and
  the archive fails on the first object written to a missing bucket.
- **The haproxy agent is not a sidecar.** It cannot move to a separate container: the configuration
  check needs the binary that serves traffic.
