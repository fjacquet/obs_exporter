# Swagger 4.2 validation findings — three live-verify items

## Status

Accepted (2026-06-14). **Closed 2026-07-29**: all three items were verified
against a live cluster and none required a code change.

The verification cluster ran ObjectScale **4.3.0.0**, not the 4.1.0.0 this ADR
targets. That is enough to close the three findings — each was a swagger-vs-code
discrepancy about a request shape or a path, and the live cluster answered each
one directly — but it does not make 4.1 a tested version. Nothing here should be
read as "4.1 verified".

## Context

We validated the implementation against the bundled management-API swagger
`docs/swagger/6972-4.1.0.json`. Two facts shape what the validation can prove:

- The artifact is titled **"OBS MGT REST API 4.2"** — a superset of the 4.1.0.0
  target (see ADR-0007).
- Every response body in the swagger has an **empty schema** (`type: object`,
  `properties: {}`), consistent with the payload weirdness recorded in ADR-0007.
  Response-field→metric mappings therefore remain fixture-derived and cannot be
  checked against the spec.
  A concrete instance surfaced in v2.7.0: the nodes and replication-group HAL
  arrays are keyed `_instances` on real clusters and `instances` in the reference
  examples, and the swagger contains neither — its only `instances` token is the
  unrelated path `/vdc/instances/storageservers`.

All seven management endpoints the exporter calls exist in the swagger with matching
methods and auth, **except** as noted below. The Grafana dashboard references only
emitted metrics (no broken panels), and `docs/metrics/` is complete.

Three discrepancies were found that the swagger *can* express (request shape / path)
but which could not be resolved from the 4.2 artifact alone, because ADR-0007
establishes the swagger is unreliable on payloads and the target is 4.1. Changing them
blind risked breaking a working integration, so they were recorded here for
verification against a live cluster rather than acted on. Each now carries its
resolution.

## Findings

### F1 (HIGH) — billing request body shape

`internal/ecs/metering.go` sends `billingBulkReq{ID}` → JSON `{"id":[...]}` to
`POST /object/billing/namespace/info`. The swagger documents the body as
`{"namespace_list":{"id":[...]}}` (declared `application/xml`). Our tests pass because
`cmd/mockecs` returns the billing fixture without validating the request body.

**Impact if real:** `ecs_namespace_used_bytes`, `_objects`, and `_mpu_*` are silently
absent on a live cluster.

**Resolution (2026-07-29): not a bug — no change.** On the live 4.3 cluster,
`POST /object/billing/namespace/info?sizeunit=KB` with our unwrapped `{"id":[...]}`
body returned **200** and a populated `namespace_billing_infos` array: 54 entries
for 54 namespaces, 11 of them with non-zero usage, every entry carrying
`valid_namespace: "true"`. The swagger's `{"namespace_list":{"id":[...]}}` wrapper
is not required. The exporter emitted `ecs_namespace_used_bytes`, `_objects`,
`_mpu_used_bytes` and `_mpu_parts` for all 54 namespaces in the same run.

This was the item worth worrying about: had the swagger been right, every
namespace metering metric would have been silently absent on every deployment,
and no test could have caught it, because `cmd/mockecs` does not validate request
bodies. That gap in the mock is real and remains — it is simply not being
exercised by a wrong request shape today.

### F2 (MEDIUM) — `/vdc/nodes` absent in the 4.2 swagger

`internal/ecs/info.go` (→ `ecs_cluster_info` version) and `internal/ecs/dt.go` (node
enumeration) call `GET /vdc/nodes`. The 4.2 swagger does not list it; it lists
`/vdc/vdc/nodes` and `/vdc/nodes/geo` instead. This may be a 4.1→4.2 relocation or a
swagger path-doubling quirk.

**Impact if real:** `ecs_cluster_info` and the entire opt-in DT collector fail on the
target cluster.

**Resolution (2026-07-29): not a bug — no change.** `GET /vdc/nodes` returned
**200** on the live 4.3 cluster, with the expected shape
`{"node":[{"nodename":…,"mgmt_ip":…,"data_ip":…,"nodeid":…}]}`. No relocation to
`/vdc/vdc/nodes`. `ecs_cluster_info` was emitted with
`version="4.3.0.0.142978.ab620a08b0b8"` in the same run. The 4.2 swagger's
omission of the path is a swagger defect, not an API change.

### F3 (LOW) — billing content-type

The client sends JSON (`ForceContentType("application/json")` for the response); the
swagger documents the billing request as `application/xml`. ECS likely tolerates JSON
(the client already compensates for ECS content-type quirks), but this is unverified.

**Resolution (2026-07-29): not a bug — no change.** The JSON request body was
accepted; see F1's 200 with a fully populated response. No XML is needed.

## How the verification was done

The PR #18 contributor ran the exporter against their ObjectScale 4.3.0.0 cluster
with the client's `Trace` mode (`ecsclient.Config.Trace`) enabled, and supplied
the sanitized trace and debug logs. The run covered 61 requests across all seven
endpoints the exporter calls, on a cluster with 5 nodes and 54 namespaces. Every
request returned 200, all five collectors reported `ecs_collector_up 1`, and
`ecs_up` was 1.

Two things worth recording from the same run, outside this ADR's three findings:

- The exporter's own output is consistent with the ECS capacity model:
  `allocated + free + reserved` equalled `total` to the byte, with reserved at
  exactly 10% of total.
- The per-node payload carried no `nodeCpuUtilization`, `nodeMemory*` or NIC
  fields, and the cluster payload carried no `transaction*` fields, on a cluster
  where the reference documents all of them. `docs/metrics/` records this as an
  availability caveat; the Flux API is the only known source for those.

## Consequences

- **No code change resulted from any of the three findings.** The exporter's
  billing request shape, its `/vdc/nodes` path and its JSON content-type were all
  correct as written; the 4.2 swagger was wrong or incomplete on each.
- This is the third distinct way the bundled swagger has proven unreliable, after
  the empty response schemas and the HAL list key recorded above. Treat it as a
  hint about which endpoints exist, never as a source of truth about request or
  response shapes. Live verification, or a captured payload, is the only evidence
  that settles a shape question in this project.
- `cmd/mockecs` still does not validate request bodies, so a future change to a
  request shape would pass the suite unnoticed. Nothing depends on that today —
  F1 closed the one case where it mattered — but the gap is real and worth
  closing if another request shape ever comes into question.
