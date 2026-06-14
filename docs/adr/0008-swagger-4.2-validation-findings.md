# Swagger 4.2 validation findings — three live-verify items

## Status

Accepted (2026-06-14). Tracks open verification items; supersedes nothing.

## Context

We validated the implementation against the bundled management-API swagger
`docs/swagger/6972-4.1.0.json`. Two facts shape what the validation can prove:

- The artifact is titled **"OBS MGT REST API 4.2"** — a superset of the 4.1.0.0
  target (see ADR-0007).
- Every response body in the swagger has an **empty schema** (`type: object`,
  `properties: {}`), consistent with the payload weirdness recorded in ADR-0007.
  Response-field→metric mappings therefore remain fixture-derived and cannot be
  checked against the spec.

All seven management endpoints the exporter calls exist in the swagger with matching
methods and auth, **except** as noted below. The Grafana dashboard references only
emitted metrics (no broken panels), and `docs/metrics.md` is complete.

Three discrepancies were found that the swagger *can* express (request shape / path)
but which we cannot resolve from the 4.2 artifact alone, because ADR-0007 establishes
the swagger is unreliable on payloads and the target is 4.1. Changing them blind risks
breaking a working integration. They are recorded here for verification against a live
4.1 cluster using the client's `Trace` mode (`ecsclient.Config.Trace`), which logs
method/path/status/body without leaking the auth token.

## Findings

### F1 (HIGH) — billing request body shape

`internal/ecs/metering.go` sends `billingBulkReq{ID}` → JSON `{"id":[...]}` to
`POST /object/billing/namespace/info`. The swagger documents the body as
`{"namespace_list":{"id":[...]}}` (declared `application/xml`). Our tests pass because
`cmd/mockecs` returns the billing fixture without validating the request body.

**Impact if real:** `ecs_namespace_used_bytes`, `_objects`, and `_mpu_*` are silently
absent on a live cluster.

**Disposition:** verify with `Trace` against a live 4.1 cluster. If the wrapper is
required, wrap the request struct and update `cmd/mockecs` + fixtures, then remove this
item.

### F2 (MEDIUM) — `/vdc/nodes` absent in the 4.2 swagger

`internal/ecs/info.go` (→ `ecs_cluster_info` version) and `internal/ecs/dt.go` (node
enumeration) call `GET /vdc/nodes`. The 4.2 swagger does not list it; it lists
`/vdc/vdc/nodes` and `/vdc/nodes/geo` instead. This may be a 4.1→4.2 relocation or a
swagger path-doubling quirk.

**Impact if real:** `ecs_cluster_info` and the entire opt-in DT collector fail on the
target cluster.

**Disposition:** verify `GET /vdc/nodes` still resolves on the live 4.1 cluster. If it
404s, switch the path (and confirm the response still carries `node[].version` /
`mgmt_ip` / `data_ip`).

### F3 (LOW) — billing content-type

The client sends JSON (`ForceContentType("application/json")` for the response); the
swagger documents the billing request as `application/xml`. ECS likely tolerates JSON
(the client already compensates for ECS content-type quirks), but this is unverified.

**Disposition:** confirm the live cluster accepts the JSON request body; no change if
it does.

## Consequences

- No code changes are made now; the three items are tracked here until a live 4.1
  cluster is available for `Trace`-mode verification.
- When verified, fix or close each item and update this ADR's status.
