# Configuration

The exporter reads a single YAML file (`--config config.yaml`). Reference:

```yaml
server:
  host: "0.0.0.0"   # bind address
  port: "9438"      # bind port
  uri: "/metrics"   # Prometheus endpoint path

collection:
  interval: "5m"    # how often every cluster is polled
  timeout: "60s"    # per-cluster budget within a cycle

# Optional OTLP gRPC metric push. Disabled while endpoint is empty.
otlp:
  endpoint: "otel-collector:4317"
  insecure: true    # plaintext gRPC (use false + TLS in production)
  interval: "10s"   # push cadence

clusters:
  - name: ecs-prod-01            # the `cluster` label value (defaults to host)
    host: ecs01.example.com
    mgmtPort: 4443               # management API port
    username: ecs-monitor
    password: "${OBS1_PASSWORD}"  # ${ENV_VAR} works in host, username, and password
    # passwordFile: /run/secrets/ecs01  # alternative to password
    insecureSkipVerify: false    # self-signed certs (dev/test only)
    collectMetering: true        # namespace quota + billing (default true)
    collectDT: false             # opt-in legacy node-local DT scraping
    # objPort: 9021              # only used by collectDT
    # dtPort: 9101               # only used by collectDT
```

## Secrets

`${ENV_VAR}` references are interpolated in **host**, **username**, and **password**
at config-load time. A referenced variable that is not set causes an immediate error
(fail fast — a typo in a secret name shows up at startup, not as repeated auth
failures).

Passwords additionally support a file-based alternative:

1. `${ENV_VAR}` inside `password` — variable must be set.
2. `passwordFile` — read and trimmed when `password` resolves empty.

### Single-cluster vs multi-cluster

`${ENV_VAR}` references are a **single-cluster convenience**: put the env ref in
`config.yaml` (e.g. `host: "${OBS1_HOSTNAME}"`), export the variable, and you
avoid editing the file for each environment.

`config.yaml` is always the source of truth and is always consumed. For
**multi-cluster** setups use one `clusters` entry per cluster, either with literal
values or with per-cluster env refs (e.g. `${OBS1_PASSWORD}`, `${OBS2_PASSWORD}`)
— there is no implicit discovery of clusters from env vars.

## Hot reload

The exporter reloads the config on **SIGHUP** or when the file changes on disk
(temp-file + rename updates are detected). A reload rebuilds the clients and the
collection loop and runs an immediate cycle; an invalid file is logged and ignored,
keeping the running config. Changes to `server.*` need a restart.

## Per-cluster collector flags

| Flag | Default | Effect |
|---|---|---|
| `collectMetering` | `true` | namespace list + quota + bulk billing. Disable on very large clusters if the billing query is slow. |
| `collectDT` | `false` | legacy node-local DT/connection stats over ports 9101/9021 (undocumented ECS internals, v1 parity). |

## Prometheus scrape config

The v1 `/query?target=` pattern is gone — point Prometheus at `/metrics`:

```yaml
scrape_configs:
  - job_name: ecs
    static_configs:
      - targets: ["obs-exporter.example.net:9438"]
```
