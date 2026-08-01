# Docker

The published image is `ghcr.io/fjacquet/obs_exporter`. Tags follow the release
version (`3.2.0`), plus `latest`.

It is built `FROM alpine:latest` and contains the exporter binary, the Alpine/
busybox userland (a shell, `wget`, and the rest of the base image), and its CA
bundle. Unlike a distroless image, you *can* `docker exec -it … sh` into a
running container to look around, and the image ships a Docker `HEALTHCHECK`
(see [Health check](#health-check) below) that runs `wget` against `/livez`
from inside the container every 30 seconds.

It runs as the `obs` user (uid 10001), so a mounted `config.yaml` must be
world-readable or owned by uid 10001. A config file the container cannot read
is a config file the exporter cannot load, and it refuses to start rather than
run without one — so the symptom is a container that exits within a second of
`docker run`, after the same file worked fine for a local binary you ran as
yourself.

## Run against a real cluster

The exporter needs one file: `config.yaml`. The image's default command already
points at `/etc/obs_exporter/config.yaml`, so mounting it there is enough — no
arguments required. Pass secrets as environment variables, which `config.yaml`
references as `${VAR}`.

```bash
docker run -d --name obs_exporter \
  -p 9438:9438 \
  -v "$PWD/config.yaml:/etc/obs_exporter/config.yaml:ro" \
  -e OBS1_PASSWORD \
  ghcr.io/fjacquet/obs_exporter:3.2.0
```

Pin the version tag in production. `latest` moves on every release, and a
metric rename between majors would arrive unannounced on the next pull.

To pass flags, put them after the image name — but they **replace** the image's
default command rather than adding to it. That default command is the only thing
supplying `--config /etc/obs_exporter/config.yaml`, so the moment you pass flags
of your own you have to repeat it, or the exporter falls back to the flag's
default of `config.yaml` relative to the container's root directory, fails to
find it, and exits 1:

```bash
docker run --rm \
  -v "$PWD/config.yaml:/etc/obs_exporter/config.yaml:ro" \
  -e OBS1_PASSWORD \
  ghcr.io/fjacquet/obs_exporter:3.2.0 \
  --config /etc/obs_exporter/config.yaml --once --debug
```

That is the fastest connectivity check against a new cluster: one collection
cycle, every sample printed, then exit. See [Verify and
troubleshoot](../operate/troubleshooting.md) for the `--once --debug --trace`
workflow.

Check it came up:

```bash
curl -s localhost:9438/metrics | grep '^ecs_up'
curl -s localhost:9438/health | jq .
```

## Health check

The image ships a Docker `HEALTHCHECK`:

```
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --quiet --tries=1 --spider http://127.0.0.1:9438/livez || exit 1
```

That's enough for `docker ps` to show `(healthy)`/`(unhealthy)` and for tools
that key off Docker's own health state (e.g. `depends_on: condition:
service_healthy` in Compose). It is **not** what Kubernetes uses — the kubelet
never reads a Docker `HEALTHCHECK`; it always probes independently via the
chart's own `livenessProbe`/`readinessProbe`. See
[Kubernetes](kubernetes.md#probes) for that path.

Point any check — the image's `HEALTHCHECK`, a Kubernetes probe, or your own
monitoring — at **`/livez`** or **`/readyz`**. Both always answer 200 —
neither depends on cluster reachability or on the collection cycle having run
at all, so neither can flag a healthy process as failing over a cluster that
happens to be unreachable, which no restart could fix anyway.
[ADR-0013](../adr/0013-static-liveness-readiness-probes.md) has the full
argument. Because they don't wait on the first collection cycle, there's no
startup window to cover for them.

`/health` still exists and always answers 200, with a JSON body naming every
cluster's status (`ok`/`err` per cluster). It's not what an external health
check should use, but it's the right endpoint for a human checking in, or for
a monitoring system that wants to know *which* cluster is degraded — read the
body, not the status code ([ADR-0015](../adr/0015-health-always-200.md)).
Alert on `ecs_up` and `ecs_collector_up` rather than on any probe endpoint.
[Verify and troubleshoot](../operate/troubleshooting.md#checking-health-without-scraping)
sets out the full argument and what the `/health` JSON body contains.

`/metrics` carries only `obs_exporter_build_info` until the first collection
cycle finishes — the HTTP server deliberately comes up before that cycle
completes. That window's length is bounded by `collection.timeout` — 60
seconds by default — not by `collection.interval`.

## Compose

`docker-compose.yml` in the repo root is the **demo** stack — it starts
`mockecs`, a fake ECS API, alongside Prometheus and Grafana, and is meant for
`make demo`. Do not deploy it. For a real cluster, a minimal service looks like:

```yaml
services:
  obs_exporter:
    image: ghcr.io/fjacquet/obs_exporter:3.2.0
    command: ["--config", "/etc/obs_exporter/config.yaml"]
    ports:
      - "9438:9438"
    volumes:
      - ./config.yaml:/etc/obs_exporter/config.yaml:ro
    environment:
      - OBS1_PASSWORD
    restart: unless-stopped
```

Compose reads `.env` from the working directory natively, so `OBS1_PASSWORD`
declared without a value is taken from there. See `.env.example`.

## Config reload

The exporter watches its config file and reloads on change, so
`docker cp` or an updated bind mount takes effect without a restart. `SIGHUP`
works too:

```bash
docker kill -s HUP obs_exporter
```

A reload rebuilds the clients and the collection loop; `/metrics` keeps serving
the last snapshot throughout. An invalid file is logged and ignored, and the
running config stays in place.

## Logs

The exporter writes all of its logging to standard error and never to a file, so
`docker logs obs_exporter` shows everything it has to say. There is nothing to
configure and no log path to set.

The one thing worth knowing is that standard *output* is used for something
else: `--debug` combined with `--once` prints every collected sample there, so
in a throwaway diagnostic container the two streams separate cleanly with a
shell redirect. [Verify and troubleshoot](../operate/troubleshooting.md) uses
that split.
