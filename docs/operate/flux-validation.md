# Flux live-cluster validation

The [Flux collector](../metrics/flux.md) is validated against real ObjectScale
clusters by email, on a months-long round trip through the project's only
live-cluster contact. The 2026-07-31 capture that closed
[ADR-0011](../adr/0011-flux-collector-for-unreachable-metrics.md)'s deferred
verification was assembled by hand from ten separate queries; this page exists
so the next round is two commands and a reply, not a campaign.

There is no config or credential this needs beyond what already runs the
exporter. Everything below reads from the cluster; nothing writes to it.

## Run these two commands

### 1. Capture every Flux measurement, raw

```bash
obs_exporter flux-capture --config config.yaml --out flux-capture
```

This replays the collector's own query table — the same ten queries `Collect`
issues, not a hand-written approximation — against one configured cluster, and
writes one JSON file per measurement plus a `summary.json` (rows read, per
measurement) into `--out`. Flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--config` | `config.yaml` | path to the config file |
| `--cluster` | *(first configured)* | which cluster to query, by name |
| `--out` | `flux-capture` | directory to write responses into |
| `--bucket` / `--measurement` | *(empty)* | used together, to probe one measurement outside the query table |
| `--trace` | `false` | also log every response body as it arrives, not just what gets written to a file |

To probe a measurement this collector does not query — for one of the [open
questions](#what-this-round-needs-to-answer) below, or anything else worth a
look — supply `--bucket` and `--measurement` together:

```bash
obs_exporter flux-capture --config config.yaml \
  --bucket monitoring_op --measurement diskio --out flux-capture
```

### 2. Run one full collection cycle, traced

```bash
obs_exporter --config config.yaml --once --trace --debug
```

`--once`, `--trace` and `--debug` are flags of the **root** command, not of
`flux-capture` — the two commands do not combine, and passing them to
`flux-capture` fails with an unknown-flag error. This invocation runs one
cycle across **every** collector, not just Flux: `--trace` logs every
management API response body (each Flux query's body included), `--debug`
additionally prints every sample the cycle produced, and `--once` exits
afterward instead of starting the HTTP server. It is slower to read than
`flux-capture`'s output and answers a different question — not "what did the
store return" but "what did the collector make of it" — which is the other
half of diagnosing anything that looks wrong. See [the diagnostic
flags](troubleshooting.md#the-diagnostic-flags) for what each one does on its
own.

## Sanitize before sending anything

`flux-capture` writes responses **verbatim** — real hostnames, IP addresses
and namespace names, straight out of the cluster's own monitoring store — and
nothing in this repository redacts them automatically. Sanitizing is the
reporter's own call, on the reporter's own data policy: half-done automatic
redaction is worse than none, so this exporter ships none. The default `--out`
directory (`flux-capture`) is gitignored for exactly this reason — never
`git add -A` it into a commit.

Use the substitution-table method in [Sharing a trace
safely](troubleshooting.md#sharing-a-trace-safely) — one real identifier, one
consistent replacement, applied to every file — for both the `flux-capture`
JSON files and the `--trace` output from the second command. The same rule
that makes a trace diagnosable applies here: replacing every IP with `x.x.x.x`
collapses five nodes into one string and destroys the thing a per-node
capture exists to show.

## What to send back

1. **The exporter version** — `obs_exporter --version`, or
   `obs_exporter_build_info{version}` from a running instance.
2. **The ObjectScale version** — `ecs_cluster_info{version}`, from the same
   `--debug` run.
3. **The sanitized `flux-capture` output directory** — every measurement's
   raw JSON, plus `summary.json`.
4. **The sanitized trace/debug output**, if you can spare it, from the
   `--once --trace --debug` run — it shows what the collector actually
   produced from that payload, which the raw capture alone does not.

## What this round needs to answer

Carried from the 2026-07-31 capture. None of them block anything already
shipped; all of them are open in [ADR-0011](../adr/0011-flux-collector-for-unreachable-metrics.md)'s
Consequences section.

1. **Real payloads for `cq_performance_transaction` and
   `cq_performance_throughput`.** Both are confirmed to emit, in prose, from
   the last round — but no payload was attached for either, so their fixtures
   in this repository are hand-written from the shape the other
   `monitoring_vdc` captures establish rather than read off a real response.
   A plain `flux-capture` run (no `--bucket`/`--measurement`) already queries
   both; it is the two files this round is missing, not a new command.
2. **The unit of the latency histogram's bucket bounds**
   (`statDataHead_performance_internal_latency`). Assumed milliseconds —
   consistent with `+Inf` sitting at 60000 and with the existing
   `ecs_node_transaction_latency_milliseconds` name — but the store does not
   document it anywhere the reporter or this repository has found.
3. **The meaning of the generic `tag` column** (seen carrying values like
   `system`, `dashboard` and `dt` across several measurements).
4. **Whether `*_Process_status` and `diskio` are absent on this specific
   build, or absent everywhere.** The 2026-07-31 capture found neither, for
   reasons unrelated to the acceptance cluster's lack of production load —
   probably not collected on that build at all, but unconfirmed either way.
   `--bucket`/`--measurement` (above) is how to probe for them directly.

See [ADR-0011](../adr/0011-flux-collector-for-unreachable-metrics.md) and the
[2026-07-31 validation
design](../superpowers/specs/2026-07-31-flux-live-validation-design.md) for
the full history behind these questions.
