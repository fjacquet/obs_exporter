# Docker

The published image is `ghcr.io/fjacquet/obs_exporter`. Tags follow the release
version (`3.2.0`), plus `latest`.

It is built `FROM gcr.io/distroless/static:nonroot` and contains the static
binary and nothing else — **no shell, no `curl`, no package manager**. That is a
deliberate trade: an image with nothing in it has almost nothing to exploit and
almost nothing to patch, and it never appears in a CVE report for a package the
exporter does not use. What you give up is the two habits you would otherwise
reach for, and both come back later on this page. You cannot `docker exec -it …
sh` into a running container to look around, because there is no shell for
`exec` to start; debugging means running a second throwaway container with
diagnostic flags instead. And you cannot write a Docker `HEALTHCHECK`, because
that instruction runs a command *inside* the container and there is no command
in there to run — the health check has to come from outside.

It runs as the `nonroot` user, so a mounted `config.yaml` must be world-readable
or owned by uid 65532. A config file the container cannot read is a config file
the exporter cannot load, and it refuses to start rather than run without one —
so the symptom is a container that exits within a second of `docker run`, after
the same file worked fine for a local binary you ran as yourself.

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

`/health` returns per-cluster JSON and **HTTP 503 while any cluster is failing**.

Docker's own `HEALTHCHECK` cannot use it, for the reason given at the top of
this page: the instruction runs a command *inside* the container, and this image
has no shell and no HTTP client to run. Probe it from outside instead —
Kubernetes probes, a Compose-level external check, or your monitoring system.

Which endpoint you probe then matters, because the two answer different
questions and only one of them is a fair test of whether the process should be
restarted:

- **`/health`** reports whether collection is succeeding. One unreachable
  cluster turns the whole endpoint 503, even though the exporter is still
  collecting and serving every other cluster in the config. Wire that to a
  restart and one bad cluster takes down the metrics for all the healthy ones —
  and the restart cannot fix anything, because a fresh process has exactly the
  same network path to the cluster that is not answering.
- **`/metrics`** reports only that the process is up and serving, which is what
  liveness is supposed to mean. It answers 200 while a cluster is failing,
  carrying `ecs_up 0` for that cluster in the body.

That is why the usual split is `/metrics` for liveness, `/health` for readiness,
and `ecs_up` / `ecs_collector_up` for alerting. The exporter is built to degrade
one cluster and one collector at a time rather than go dark, so the thing you
want to be told about is which part is degraded — which is what those two
metrics say and what a restart cannot change.

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
