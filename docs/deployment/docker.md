# Docker

The published image is `ghcr.io/fjacquet/obs_exporter`. Tags follow the release
version (`3.2.0`), plus `latest`.

It is built `FROM gcr.io/distroless/static:nonroot` and contains the static
binary and nothing else — **no shell, no `curl`, no package manager**. That
shapes two things below: you cannot `docker exec` into it to debug, and you
cannot write a Docker `HEALTHCHECK` that runs a command inside it. It runs as
the `nonroot` user, so a mounted `config.yaml` must be world-readable or owned
by uid 65532.

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

To pass flags, append them — they go to the binary's entrypoint:

```bash
docker run --rm \
  -v "$PWD/config.yaml:/etc/obs_exporter/config.yaml:ro" \
  -e OBS1_PASSWORD \
  ghcr.io/fjacquet/obs_exporter:3.2.0 --once --debug
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

`/health` returns per-cluster JSON and **HTTP 503 while any cluster is failing**.

Docker's own `HEALTHCHECK` cannot use it: the instruction runs a command *inside*
the container, and this image has no shell and no HTTP client. Probe it from
outside instead — Kubernetes probes, a Compose-level external check, or your
monitoring system.

Which endpoint to probe is a real choice:

- **`/health`** reflects collection success. In a multi-cluster process one
  unreachable cluster turns the whole endpoint 503, so using it as a liveness
  probe lets a single bad cluster restart a container that is otherwise serving
  every other cluster correctly.
- **`/metrics`** reflects only that the process is up and serving, which is what
  liveness should mean. It answers 200 even while a cluster is failing.

The usual split is `/metrics` for liveness, `/health` for readiness, and
`ecs_up` / `ecs_collector_up` for alerting — the exporter is designed to
degrade per cluster and per collector rather than go dark, and restarting it
does not fix an unreachable cluster.

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

Set `logName: ""` in `config.yaml` so the exporter logs to stdout rather than a
file — otherwise `docker logs` shows nothing useful.
