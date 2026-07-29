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
  /dashboard/zones/localzone/nodes` (`_embedded._instances[]`) through the
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
  "Current" means the newest point, not the newest point that happens to parse:
  `Latest` picks the maximum `t` first and only then reads its value, so an
  unreadable newest reading yields absence rather than the value of an older
  point. This is the same rule on the time axis — the exporter publishes these as
  live gauges, and once a stale reading reaches Prometheus nothing distinguishes
  it from a current one.
- **Tolerant HAL list decoding**: the nodes and replication-group endpoints return
  their arrays under `_embedded._instances`, which is what real clusters emit (ECS
  3.8 through ObjectScale 4.3, field-confirmed). The Dell reference examples show
  `_embedded.instances` instead, and the bundled swagger cannot arbitrate — every
  response body in it declares an empty schema (ADR-0008). Both spellings are
  therefore accepted by a shared `halList[T]` decoder, which also records whether
  either key was present so an unrecognised shape logs a warning instead of
  silently yielding zero instances.
- New 4.1 data exported: maintenance/ready-to-replace counts, per-RG
  `replicationRpoLag`, per-node CPU/memory/NIC.

## Consequences

- Metering cost per cycle drops from O(namespaces) billing calls to one POST.
- Default deployments need only the management port (4443) open.
- The fixture suite is derived from the 4.1 reference examples. Since v2.7.0 it
  carries targeted corrections where a live ObjectScale 4.3 cluster contradicted
  the reference — the HAL list key, the cluster-level replication-traffic shape —
  plus synthetic entries covering node health states the examples omit. Where the
  reference and an observed payload disagree, the observed shape wins; the
  fixtures are not captured payloads and should not be read as hardware ground
  truth.

## Related

- [Swagger 4.2 validation findings](0008-swagger-4.2-validation-findings.md) —
  open live-verify items (billing body, `/vdc/nodes`, content-type).
