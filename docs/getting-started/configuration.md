# Configure

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

# Optional custom labels stamped onto every exported sample, ecs_up included.
# The top-level block declares the KEYS with their default values; a cluster
# may override a VALUE but never introduce a key. See "Custom labels" below.
# labels:
#   env: prod
#   site: geneva
#   owner: ${TEAM_NAME}

clusters:
  - name: ecs-prod-01            # the `cluster` label value (defaults to host)
    host: ecs01.example.com
    mgmtPort: 4443               # management API port
    username: ecs-monitor
    password: "${OBS1_PASSWORD}"  # ${ENV_VAR} works in host, username, password, and insecureSkipVerify
    # passwordFile: /run/secrets/ecs01  # alternative to password
    insecureSkipVerify: false    # self-signed certs (dev/test only); also accepts ${OBS1_SKIP_CERTIFICATE}
    collectMetering: true        # namespace usage via bulk billing (default true)
    collectQuotas: true          # per-namespace quota limits, own collector (default true)
    collectDT: false             # opt-in legacy node-local DT scraping
    collectFlux: false           # opt-in Flux monitoring-store queries (needs SYSTEM_MONITOR)
    # objPort: 9021              # only used by collectDT
    # dtPort: 9101               # only used by collectDT
    # labels:
    #   site: zurich              # overrides the global value for this cluster only
```

## Secrets

`${ENV_VAR}` references are interpolated in **host**, **username**, **password**, and
**insecureSkipVerify** at config-load time. A referenced variable that is not set causes
an immediate error (fail fast — a typo in a secret name shows up at startup, not as
repeated auth failures).

`insecureSkipVerify` accepts either a native YAML boolean (`true`/`false`) or an
`${ENV_VAR}` reference (e.g. `${OBS1_SKIP_CERTIFICATE}`) that resolves to a boolean
string (`true`/`false`); it defaults to `false` when omitted.

Passwords additionally support a file-based alternative:

1. `${ENV_VAR}` inside `password` — variable must be set.
2. `passwordFile` — read and trimmed when `password` resolves empty.

### Passwords with special characters

Any character is safe end to end — the password is sent via HTTP Basic authentication
(base64-encoded in the `Authorization` header), so nothing needs URL-encoding. The only
place quoting matters is **parsing at load time**, and it differs by where you put the
password:

| Source | Rule |
| --- | --- |
| `.env`, single-quoted `'…'` | Fully literal — no `$` expansion, no `\` escapes, no `#` comment. Best default. Cannot contain a literal `'`. |
| `.env`, double-quoted `"…"` | Expands `$VAR`/`${VAR}` and processes `\` escapes. `$`, `\`, `"` are special — write `\$`, `\\`, `\"`. |
| `.env`, unquoted | `$VAR` expands; a `#` (space-hash) starts a comment; a value **starting** with `'`/`"` is treated as quoted. |
| `config.yaml` inline | Only the exact `${NAME}` token is interpolated (`os.LookupEnv`), so a literal password containing `${NAME}` is treated as an env ref. Prefer referencing an env var. |
| `passwordFile` | Read **verbatim** (only surrounding whitespace trimmed) — no interpolation, no escaping. The bulletproof option. |

For quotes inside the password specifically: use double quotes to include a `'`, single
quotes to include a `"`. If the password has **both** `'` and `"` (or a `\`, or starts
with a quote), use `passwordFile` — it needs no escaping at all. When referencing an env
var from `config.yaml` (`password: "${OBS1_PASSWORD}"`) the value is inserted verbatim
and never re-scanned, so the env var itself may contain `$`, `${…}`, or any character.

### .env loading

The exporter binary loads a `.env` file natively at startup — from the working
directory first, then next to the config file — so `cp .env.example .env` works
for bare-metal and systemd runs exactly like it does under docker compose.
Already-set environment variables **always take precedence** over `.env` values,
so secret injection (systemd `Environment=`, Kubernetes secrets, CI) can never be
shadowed by a stray file.

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
| --- | --- | --- |
| `collectMetering` | `true` | namespace usage via one bulk billing POST. Disable on very large clusters if the billing query is slow; this also disables `quotas`. |
| `collectQuotas` | `true` | the `quotas` collector (per-namespace quota limits). Requires `collectMetering`. See below. |
| `collectDT` | `false` | legacy node-local DT/connection stats over ports 9101/9021 (undocumented ECS internals, v1 parity). See the [DT reachability warning](../metrics/index.md#node-dt-opt-in-collectdt-true). |
| `collectFlux` | `false` | per-node CPU/memory/network, per-node request counters, a per-node request-latency histogram, and cluster-wide DT and transaction metrics from the cluster's Flux monitoring store; also a second, conditional source for the per-node DT count. Same management port and session as every other collector — **no extra network access** — but the account must hold `SYSTEM_MONITOR` or `SYSTEM_ADMIN`. Adds ten requests per cycle, or nine when `collectDT` is also on. See the [Flux collector page](../metrics/flux.md). |

Enabling `collectFlux` makes it the **sole source** of `ecs_node_cpu_utilization_percent`,
`ecs_node_memory_utilization_percent` and `ecs_node_memory_used_bytes` — the dashboard
path stops emitting those three, so exactly one collector produces each. It also
**displaces** `ecs_node_transaction_latency_milliseconds` (the per-node latency gauge)
with a `_bucket`/`_count` histogram under the same base name, and **conditionally**
takes over the per-node `ecs_node_dt_total` count whenever `collectDT` is off — see the
[Flux collector page](../metrics/flux.md) for the exact arbitration rules. Everything
else it emits is additive. On a 4.3 cluster the dashboard payloads do not carry the
CPU/memory/latency fields at all, which is the gap this collector exists to close; on a
cluster that *does* serve them, enabling Flux switches their source, so verify they still
appear before relying on it. `collectFlux` and `collectDT` are independent — enable
either, both, or neither.

### Quotas on clusters with many namespaces

Namespace **usage** costs one bulk billing POST per cycle regardless of namespace
count. Namespace **quotas** are different: the management API has no bulk quota
endpoint, so the `quotas` collector issues one `GET …/namespace/{ns}/quota` per
namespace per cycle. Those requests run concurrently (8 in flight), which keeps a
large cluster inside a normal collection interval, but they still hit the API.

Quotas are their own collector rather than a flag inside metering so that
`ecs_collector_up{collector="quotas"}` reports when the quota reads themselves
are failing. The cost is one extra namespace listing per cycle.

A cluster that sets no quotas gets nothing back for them — on a 55-namespace 4.3
cluster, all 55 requests returned `blockSize: -1` and `notificationSize: -1`, and
the exporter correctly emitted zero quota samples for 55 requests. Set
`collectQuotas: false` there: usage, objects and MPU metrics are unaffected, and
only `ecs_namespace_quota_hard_bytes` / `_soft_bytes` disappear — which were
already absent.

## Custom labels

```yaml
# Optional custom labels stamped onto every exported sample, ecs_up included.
# The top-level block declares the KEYS with their default values; a cluster may
# override a VALUE but never introduce a key, so every series carries the same
# label-key set. Values accept ${ENV_VAR} interpolation.
labels:
  env: prod
  site: geneva
  owner: ${TEAM_NAME}
clusters:
  - name: obs-prod-01
    # ...
    labels:
      site: zurich    # overrides the global value for this cluster only
```

Prometheus target relabeling cannot do this job: one exporter process serves
many clusters behind a single scrape target, so any label a relabeling rule
attaches applies to every series from every cluster alike. Only the exporter
itself knows which cluster produced a given sample, so per-cluster label
values have to be assigned inside it.

The split is deliberate and mirrors ADR-0006's label-key invariant — a metric
name carries exactly one ordered label-key set across all its series. The
top-level `labels:` block is where the **keys** live, each with a default
value; a cluster's own `labels:` block may only override a declared key's
**value**. An undeclared key in a cluster block is a config-load error, and a
key resolving to an empty value is rejected the same way — both by
construction, not by a completion pass, so the key set stays uniform across
every cluster the process serves. Keys must match `[a-zA-Z_][a-zA-Z0-9_]*` and
may not start with `__`; values accept `${ENV_VAR}` interpolation exactly like
`host`, `username` and `password` do.

Labels are stamped onto **every** sample, including `ecs_up` and
`ecs_collector_up`, by the same collection-loop choke point that already
stamps the `cluster` identity label — so an `env` or `site` label is never
missing from one metric while present on another.

If a custom key collides with a name a collector already uses as its own
label (for example a collector that has its own `env` dimension), the custom
key is dropped for that metric family and the exporter logs it once per key
per cluster:

```text
WARN[0000] custom label collides with a collector dimension; dropped for that metric family  cluster=ecs-prod-01 label=env metric=ecs_up
```

The collector's own dimension always wins; the collision is uniform per
metric name, so it never produces a mixed series schema for that name. See
[ADR-0014](../adr/0014-custom-labels.md) for the full decision record.

`cluster` is a reserved key: it always collides with the identity label the
collection loop stamps first, so a custom label named `cluster` is dropped
from every sample on every cycle — don't use it as a custom-label key.

### Ad hoc filters in Grafana

Custom labels can be filtered directly on dashboards using the ad hoc filters variable. Two
limits apply:

- **Ad hoc filters apply to panel queries, not to variable queries**, so the `cluster`
  picker still lists every cluster even while filtering on `env=prod`. Confirm the
  behaviour against the Grafana version in use — it has changed across releases.
- **Filters do not group**: `sum by (env)` remains the user's responsibility; ad hoc filters
  simply restrict which series match a query, they do not aggregate.

## Prometheus scrape config

The v1 `/query?target=` pattern is gone — point Prometheus at `/metrics`:

```yaml
scrape_configs:
  - job_name: ecs
    static_configs:
      - targets: ["obs-exporter.example.net:9438"]
```
