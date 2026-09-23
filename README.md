# Placitum agents

English · [Русский](README.ru.md)

Placitum monitoring agents: small processes that report the state of the infrastructure next to
them. Each one is its own Go module and its own image.

| Directory | Image | What it does |
| --- | --- | --- |
| [`redis/`](redis) | `placitum/agents-redis` | health, memory and keys of one Redis instance; presence frame on `WAF_STATUS.store.redis.<id>` |
| [`s3/`](s3) | `placitum/agents-s3` | archive buckets: availability, size, request rate; frame on `WAF_STATUS.store.s3.<id>`. Creates the buckets it is told to |
| [`haproxy/`](haproxy) | `placitum/agents-haproxy` | haproxy and its agent in one container; the configuration comes from the controller |

The first two only observe: they keep no state and do not touch traffic, so losing one leaves the
store and the installation as they are. The third is a balancer with managed configuration. Its
agent lives on the node itself, because `haproxy -c` must check the configuration with the same
binary that serves traffic. All three share one contract, the presence frame on `WAF_STATUS`, and
usually change together, so they live in one repository; a build context can be a directory.

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

## License

[Apache License 2.0](LICENSE); the attribution notice is in [NOTICE](NOTICE). This repository is
part of the Placitum open core. The inspectors are licensed separately: each inspector repository
carries the Placitum License Agreement. Versions up to 1.0.1 were released under the Placitum
License Agreement 1.1.

The s3 agent links `madmin-go`, which is under AGPL-3.0, so its built image is under AGPL-3.0 as a
whole. The source in this repository stays under Apache 2.0.
