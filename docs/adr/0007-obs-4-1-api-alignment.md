# ObjectScale 4.1 API alignment

## Status

Accepted (2026-06-10)

## Context

v1 was written against the ECS 3.x API. The exporter was realigned against the
Dell ObjectScale **4.1.0.0** management REST API reference. Findings: auth and the
core dashboard paths are unchanged; 4.1 removes nothing the exporter used;
dashboard "current" stats are time-series arrays (the `*Current` suffix is gone
from the transaction fields); numbers frequently arrive as quoted strings; and 4.1
adds a bulk billing POST plus richer dashboard endpoints.

## Decision

- **Bulk billing**: replace v1's N+1 per-namespace `GET
  /object/billing/namespace/{ns}/info` with one `POST
  /object/billing/namespace/info?sizeunit=KB` (body `{"id": [...]}`) per cycle.
  Quota still requires per-namespace GETs.
- **Documented node stats**: per-node metrics come from `GET
  /dashboard/zones/localzone/nodes` (`_embedded.instances[]`) through the
  management port — replacing v1's undocumented node-local scraping as the default
  node-metric source.
- **Opt-in DT parity**: the v1 DT/connection metrics (node-local
  `:9101/stats/dt/DTInitStat` XML and `:9021/?ping`) remain available behind
  `collectDT: false` — undocumented internals, off by default.
- **Defensive payload parsing**: a tolerant point parser (`Series`) handles the
  time-series arrays — value key varies per field (`Space`, `Bytes`, `Percent`,
  `Bandwidth`, `Latency`, `TPS`, `Count`, …), values may be numbers or strings
  (including `"N/A"`), and the newest point by `t` is taken as "current". Scalars
  use a string-or-number `Num` type; unparseable values yield *absent* samples,
  never zeros.
- New 4.1 data exported: maintenance/ready-to-replace counts, per-RG
  `replicationRpoLag`, per-node CPU/memory/NIC.

## Consequences

- Metering cost per cycle drops from O(namespaces) billing calls to one POST.
- Default deployments need only the management port (4443) open.
- The fixture suite mirrors the 4.1 reference examples, so payload-shape
  regressions are caught by tests.

## Related

- [Swagger 4.2 validation findings](0008-swagger-4.2-validation-findings.md) —
  open live-verify items (billing body, `/vdc/nodes`, content-type).
