# Placitum agents

English · [Русский](README.ru.md)

Placitum monitoring agents: small processes that report the state of the infrastructure next to
them. Each one is its own Go module and its own image.

| Directory | Image | What it does |
| --- | --- | --- |
| [`redis/`](redis) | `placitum/agents-redis` | health, memory and keys of one Redis instance; presence frame on `WAF_STATUS.store.redis.<id>` |
| [`s3/`](s3) | `placitum/agents-s3` | archive buckets: availability, size, request rate; frame on `WAF_STATUS.store.s3.<id>`. Creates the buckets it is told to |
| [`haproxy/`](haproxy) | `placitum/agents-haproxy` | not a sidecar but a node: haproxy and its agent in one container, configuration comes from the controller |

The first two only observe: they keep no state and do not touch traffic, and losing one affects
neither the store nor the installation. The third is a balancer with managed configuration. Its
agent lives on the node itself, because `haproxy -c` must check the configuration with the same
binary that serves traffic.

## Build

Each image is built from its directory:

```sh
docker build -t placitum/agents-redis redis
docker build -t placitum/agents-s3 s3
docker build -t placitum/agents-haproxy haproxy
```

From git, with the directory as the build context:

```sh
docker buildx build -t placitum/agents-redis "https://github.com/exemt/placitum-agents.git#<ref>:redis"
```

Settings for each agent are in [INSTALL.md](INSTALL.md).

## Why one repository

All three share one contract, the presence frame on `WAF_STATUS`, and usually change together: the
frame changes, not the behavior of a particular agent. Three repositories would mean three
synchronized changes each time. The installer does not mind: a build context can be a directory.

## License

[Placitum License Agreement](LICENSE.md). A Russian translation is in [LICENSE.ru.md](LICENSE.ru.md);
the English text is the legally binding one.
