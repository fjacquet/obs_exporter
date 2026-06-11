# Hand-rolled resty/v2 client instead of the goobjectscale SDK

## Status

Accepted (2026-06-10)

## Context

The family rule: *use the official vendor Go SDK if (1) available and (2) useful*.
Dell publishes `github.com/dell/goobjectscale` (active, v1.1.0, clean dependency
tree, `go 1.26` floor) — so availability holds, and it was evaluated against the
usefulness criteria.

## Decision

**Hand-roll a lean `go-resty/resty/v2` client** (`internal/ecsclient`).

goobjectscale fails the coverage criterion decisively: its v1.x surface (rewritten
for the CSI/CSM driver use case) is buckets + replication-group vpools only. It
covers **zero** of the endpoint families this exporter needs — no
`/dashboard/zones/localzone*`, no `/vdc/nodes`, no namespace quota, no
`/object/billing/*`. Even its older v0.4 `objmt` metering package targets the
ObjectScale 4.x `/object/mt/*` paths, not the ECS billing API. Adopting it would
mean importing the SDK solely for its ~100-line login helper while still
hand-writing every call and response model we actually use.

The SDK's auth design was kept as the reference: basic-auth `GET /login` capturing
`X-SDS-AUTH-TOKEN`, bounded re-login on expiry (see ADR-0004).

## Consequences

- ~150 lines of client code we own; response models live next to the collectors
  that consume them, so an API change is localized to one file.
- Revisit if Dell extends goobjectscale to the dashboard/metering surface —
  switching would be an internal refactor behind the `Client` interface.
