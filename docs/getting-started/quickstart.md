# Quick start

## Against a real cluster

```bash
export ECS01_PASSWORD='...'
cat > config.yaml <<'YAML'
clusters:
  - name: ecs-prod-01
    host: ecs01.example.com
    username: ecs-monitor
    password: "${ECS01_PASSWORD}"
YAML
obs_exporter --config config.yaml
curl -s localhost:9438/metrics | grep '^ecs_up'
```

Useful flags:

- `--once` — run a single collection cycle, log the result, and exit (connectivity check).
- `--debug` — verbose logging, including per-collector failures. Combined with
  `--once`, it also prints **every collected sample** (sorted, exposition style)
  so you can diff a live cluster against the [metrics reference](../metrics.md).
- `--trace` — log every management API response body (method, path, status,
  payload; the auth token is never logged). Use it when a metric you expect is
  absent: the exporter never guesses values, so an unexpected payload shape shows
  up as a missing sample — the trace shows what the cluster actually returned.

Validating against a real cluster:

```bash
obs_exporter --config config.yaml --once --debug --trace 2>trace.log | sort > samples.txt
# samples.txt  → every metric collected (compare with docs/metrics.md)
# trace.log    → raw API payloads for anything missing or suspicious
```

`/health` returns per-cluster JSON status (HTTP 503 when any cluster is failing) —
suitable as a container health check.

## No-hardware demo stack

The repo ships an end-to-end Compose stack: a fake ECS management API (`mockecs`)
→ the exporter → Prometheus → Grafana with a provisioned overview dashboard.

```bash
make demo          # builds everything from source
# or, using the published GHCR image:
make demo-ghcr
```

Then open <http://localhost:3000> (admin/admin) → **ObjectScale — Overview**.
Stop with `make demo-down`.
