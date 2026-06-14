# mockecs demo harness and duplicated fixtures

## Status

Accepted (2026-06-14)

## Context

The Compose demo (`make demo`) needs to light up the Grafana dashboard end to end
without real Dell ECS / ObjectScale hardware. The exporter talks to a management
REST API over TLS with `X-SDS-AUTH-TOKEN` session auth (ADR-0004), so a static
file server is not enough — the demo needs something that answers `GET /login`,
the dashboard/namespace GETs, and the bulk billing POST (ADR-0007) the way ECS
does.

There is no family precedent for this: it is an obs-novel piece of demo
tooling, so it warrants its own record rather than being inferred from the
Makefile.

## Decision

Ship `cmd/mockecs/` — a minimal fake ECS management API that serves canned JSON
from **embedded fixtures** (`//go:embed fixtures/*.json`) over **self-signed TLS**
on `:4443`, issuing the `mockecs-session-token` on basic-auth `GET /login`. It is
explicitly **not** a faithful ECS emulator and is **demo-only — never published**
(excluded from releases; see the binary list in GoReleaser/ADR-0001).

`cmd/mockecs/fixtures/` are **copies** of the canonical `internal/ecs/testdata/`
fixtures, which mirror the OBS 4.1 reference examples. The two sets are kept in
sync deliberately: the test fixtures stay importable by `internal/ecs` tests
without pulling a `cmd/` package into the test graph, while the embed directive
needs the JSON physically under `cmd/mockecs/`. "Keep in sync when adding or
changing fixtures" is the standing constraint (CLAUDE.md).

## Consequences

- `make demo` runs with zero external dependencies or credentials; the stack is
  `mockecs → exporter → Prometheus → Grafana`.
- A payload-shape change must be applied in both fixture locations; the canonical
  copy under `internal/ecs/testdata/` is the source of truth, the mockecs copy
  follows.
- mockecs is never a supply-chain or security surface for users — it is not built
  into any released artifact.

## Related

- [ObjectScale 4.1 API alignment](0007-obs-4-1-api-alignment.md) — the API
  surface (including the bulk billing POST) mockecs reproduces.
- [Token auth and retry policy](0004-token-auth-retry-policy.md) — the
  `X-SDS-AUTH-TOKEN` login flow mockecs fakes.
