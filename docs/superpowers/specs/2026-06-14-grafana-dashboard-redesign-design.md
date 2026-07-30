# Grafana dashboard redesign — layered ObjectScale set

**Date:** 2026-06-14
**Status:** Approved (design phase)
**Scope:** `grafana/dashboards/` provisioned dashboards. No exporter/metric changes.

## Problem

`grafana/dashboards/obs-overview.json` is a single 42-panel dashboard with 9
always-expanded rows. It works but is not "crispy/pro/focus/logic":

- **Kitchen sink.** One "Overview" is simultaneously health, capacity, performance,
  per-node, per-namespace, disk-maintenance, and Directory-Table debugging.
- **No hierarchy.** All rows expanded → endless scroll, nothing says where to look first.
- **One filter only** (`cluster`). Per-node / per-namespace panels become spaghetti at
  scale — no `node`/`namespace`/`group` variables.
- **No navigation.** No `links` dropdown, no data links between panels.
- **Grouping drift.** "Node transactions" is split from both "Performance" and "Nodes".

## Goal

A layered set: one on-call **Overview** (single screen, "is anything on fire?") plus
five focused drill-downs, sharing one visual grammar and linked by nav + data links.
Audience: ops / on-call triage.

## Design philosophy (applied to every panel)

- **RED** for traffic (Rate / Errors / Duration); **USE** for nodes
  (Utilization / Saturation / Errors); **capacity runway** for storage.
- Traffic-light thresholds (green / amber / red) with explicit, unit-correct field configs.
- **Absent ≠ zero** (ADR-0007): `connectNulls: false`, null-as-null, no `rate()` on the
  per-second gauges (CLAUDE.md).
- Compact legends: table mode, `last` + `max` calcs, placement right (multi-series) or hidden (single).
- Fixed datasource `{"type":"prometheus","uid":"prometheus"}` (matches provisioning).
- Defaults preserved: time `now-6h`, refresh `30s`.
- Every dashboard carries a `links` dropdown (Grafana `dashboards` link by shared tag)
  so the six are navigable as one set.

## Topology

Grafana file provisioning loads any JSON in `/var/lib/grafana/dashboards`, so new files
are picked up with no config change.

| Dashboard | uid | Purpose | Variables |
| --- | --- | --- | --- |
| **Overview** | `obs-overview` (kept) | On-call triage, single screen | `cluster` |
| **Performance** | `obs-performance` | Transaction RED + per-node breakdown | `cluster`, `node` |
| **Nodes** | `obs-nodes` | USE per node | `cluster`, `node` |
| **Namespaces** | `obs-namespaces` | Usage / objects / quota / MPU | `cluster`, `namespace` |
| **Replication** | `obs-replication` | Ingress / pending / RPO / zones | `cluster`, `group` |
| **Maintenance & DT** | `obs-maintenance` | Disks needing attention + Directory Tables | `cluster`, `node` |

`obs-overview` keeps its uid so existing bookmarks/links survive.

### Template variables (all `multi`, `includeAll`, sorted)

- `cluster` — `label_values(ecs_up, cluster)` (existing).
- `node` — `label_values(ecs_node_healthy{cluster=~"$cluster"}, node)`.
- `namespace` — `label_values(ecs_namespace_used_bytes{cluster=~"$cluster"}, namespace)`.
- `group` — `label_values(ecs_replication_group_ingress_traffic{cluster=~"$cluster"}, rg)`
  (the replication-group label is `rg`, confirmed in `internal/ecs/replication.go` and existing legends).

## Overview layout (three bands, fits one screen)

1. **Health band** — colored `stat`s: clusters up · nodes good/bad · disks good/bad ·
   unacked alerts by severity · max RPO lag.
2. **Capacity runway** — used-% `gauge` · cluster disk used/total `timeseries` ·
   top-N namespaces by usage `bargauge` · `predict_linear`-based projected-full panel.
3. **Golden signals (RED)** — cluster error-rate % · cluster read/write latency ·
   throughput (tps + bandwidth), all traffic-lit, cluster-aggregate only.

Each Overview panel gets a **data link** to the matching drill-down (filters carried
via `var-cluster`).

## Drill-down contents (relocated from current dashboard, nothing dropped)

- **Performance** — read/write latency, bandwidth, tps, errors-by-code, error/success
  totals; per-node tps / bandwidth / latency (the former "Node transactions" row). `node` filter.
- **Nodes** — CPU %, memory % + bytes, NIC rx/tx, disk-space free, unhealthy nodes,
  active connections. `node` filter.
- **Namespaces** — used bytes, objects, quota headroom (soft/hard), MPU used, MPU parts,
  top-N by usage. `namespace` filter.
- **Replication** — ingress/egress traffic by group, pending (journal/repo/xor) bytes,
  RPO lag + timestamp, zones. `group` filter.
- **Maintenance & DT** — cluster maintenance/ready-to-replace disks, node disks needing
  attention, DT scrape up, directory-table counts. `node` filter.

## What stays the same

Every existing panel survives — relocated to its logical home. Tags (extended with a
shared nav tag), datasource, query label conventions (`cluster=~"$cluster"`), 6h/30s
defaults, and `cluster` multi+All behavior all preserved.

## Out of scope

- No exporter, metric-name, or label changes.
- No alerting rules (dashboards only).
- `cmd/mockecs` fixtures unchanged (demo still drives all panels).

## Verification

- `make demo` brings up mockecs → exporter → Prometheus → Grafana; every panel on all
  six dashboards renders data from fixtures (no "No data" except genuinely-absent opt-in DT).
- JSON validates (provisioning loads cleanly; no Grafana import errors).
- Each panel's `datasource`, units, and thresholds reviewed against this spec.
- Nav `links` dropdown navigates between all six; Overview data links drill down correctly.
