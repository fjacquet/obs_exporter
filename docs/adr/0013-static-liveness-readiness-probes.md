# Static `/livez` and `/readyz`, decoupled from cluster state

## Status

Accepted (2026-07-31). Additive; ships alongside the Flux live-validation
work. Does not supersede a prior ADR — the chart's previous `/health`-for-both
probe wiring was never itself a recorded decision, just an unexamined
default.

## Context

`/health` (unchanged by this ADR) answers 503 while any configured cluster is
unreachable, and 200 otherwise, with a JSON body describing every cluster's
status. That is the right signal for a human, a monitoring probe reading the
body, or an alerting rule. It is the wrong signal for a Kubernetes liveness or
readiness probe, and the chart shipped with both wired to it anyway
(`charts/obs-exporter/values.yaml`, prior to this change).

As a *readiness* check — the sort that decides whether traffic reaches this
pod — `/health` is defensible on one cluster and on several: pulling a
degraded instance out of rotation is reasonable. As a *liveness* check — the
sort that restarts the process — it is wrong on one cluster and worse on
several: no restart makes an unreachable cluster reachable, and with several
clusters a restart additionally drops every metric from every cluster that
was collecting fine. `docs/deployment/kubernetes.md` and
`docs/operate/troubleshooting.md` already carried this argument and told
operators to override the chart's `livenessProbe` to `/metrics` by hand — the
fix here is to stop needing the override.

Proposed by Benjamin (see ADR-0011 for context on this contact) alongside the
2026-07-31 Flux capture: probes must not depend on cluster state at all,
collapsing liveness and readiness into one check for this exporter, since
neither should ever fail for a reason a restart or a pool removal can fix.

## Decision

Two new endpoints, `/livez` and `/readyz`, each answering `200 OK` with a
fixed `ok` body — no cluster state, no `SnapshotStore` read, nothing that can
make either fail once the process is running. Both are registered before the
first collection cycle starts, alongside `/health` (ADR-0002: the HTTP server
starts serving before the first cycle so a slow login or poll doesn't look
like a dead exporter) — so unlike `/health`, they have no startup window to
wait out either.

The chart's `livenessProbe` and `readinessProbe` defaults now point at
`/livez` and `/readyz` respectively. `/health` is unchanged: same path, same
200/503 behavior, same JSON body, still the right endpoint for a human or an
alerting rule that wants to know which cluster is degraded.

No `startupProbe` is added. The chart didn't define one before this change,
and `/livez`/`/readyz` have no startup window to cover, unlike the `/health`
based probes they replace.

## Consequences

- A fresh `helm install` with no probe overrides now gets correct behavior by
  default. Anyone who already overrode `livenessProbe` to `/metrics` per the
  prior docs advice is unaffected — their override still works, it's just no
  longer necessary.
- `/health`'s consumers — anything scripting against its status code or body
  today — see no change.
- Alerting on cluster reachability still means `ecs_up` and
  `ecs_collector_up`, or reading `/health`'s JSON body directly. `/livez` and
  `/readyz` will never reveal a degraded cluster; that was never their job.

## Related

- [ADR-0002](0002-prometheus-snapshot-model.md) — the snapshot model this
  reuses: server up before the first cycle.
- [ADR-0011](0011-flux-collector-for-unreachable-metrics.md) — prior context
  on Benjamin as the live-cluster channel.
- `docs/deployment/kubernetes.md` §Probes and
  `docs/operate/troubleshooting.md` §Checking health without scraping —
  operator-facing guidance, updated in the same change.
