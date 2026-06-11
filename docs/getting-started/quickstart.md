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
- `--debug` — verbose logging, including per-collector failures.

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
