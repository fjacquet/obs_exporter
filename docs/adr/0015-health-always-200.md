# `/health` always answers 200

## Status

Accepted (2026-08-01). Additive; follows ADR-0013. Does not supersede it —
ADR-0013 covered `/livez` and `/readyz`, this covers `/health` itself.

## Context

ADR-0013 established that an exporter is a probe: "cluster unreachable" is
data it reports, not a failure of the exporter process, and no HTTP endpoint
should encode that fact as a non-200 status where something downstream might
treat the exporter as down. It applied that argument to `/livez` and
`/readyz`. `/health` (`main.go`'s `healthHandler`) still answered 503 while
any configured cluster was unreachable — the same coupling, one level
removed, on the one endpoint documented for humans, dashboards, and manual
`curl` checks rather than for Kubernetes probes specifically.

Nothing in the chart wires `/health` to a probe anymore (ADR-0013 fixed
that), but the 503 remained a trap for anything else that treats a non-200
response as "exporter is down" rather than "exporter is telling you a
cluster is down" — a monitoring script's health check, a load balancer's
passive health check, a dashboard's uptime tile.

## Decision

`healthHandler` (`main.go`) always writes `200 OK`. The JSON body is
unchanged: `built_at`, and `clusters: [{cluster, ok, last_scrape, err}]` per
configured cluster. The per-cluster `ok`/`err` fields are now the only
channel for "which cluster is down and why" — the status code no longer
duplicates that signal, and nothing that reads the body loses information.

## Consequences

- Anything that gated on `/health`'s HTTP status code (rather than parsing
  the body) now sees 200 unconditionally and must read `ok`/`err` per
  cluster instead. Not a breaking change to the JSON shape — the fields
  were already there.
- `docs/deployment/kubernetes.md` and `docs/operate/troubleshooting.md`
  updated to stop describing `/health` as ever answering 503.
- Alerting guidance is unchanged: alert on `ecs_up`/`ecs_collector_up`, not
  on any HTTP status code, per ADR-0013 and the CLAUDE.md family standard.
