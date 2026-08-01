# Standardize container base image on Alpine

## Status

Accepted (2026-08-01)

## Context

The exporter family had two published-image patterns — Alpine (5 repos) and
`gcr.io/distroless/static:nonroot` (3 repos: pmax_exporter, ppdd_exporter,
ppdm_exporter) — as undocumented per-repo author choice, with no written
criterion (`exporter-standards` skill previously read "canonical: Alpine,
ppdd's distroless is an accepted variant", written when only one repo had gone
distroless). obs_exporter additionally had internal drift: its local
`./Dockerfile` was distroless while its published `Dockerfile.goreleaser` was
already Alpine — confirmed by pulling and inspecting the published image
directly, not by reading Dockerfile text alone.

Alpine has a shell and `wget`, so it can carry a Docker `HEALTHCHECK` and a
Compose `healthcheck:` pointed at `/livez` (ADR-0013); distroless cannot — no
shell, no HTTP client inside the image.

## Decision

Alpine is the sole standard, family-wide, for both the published
(`Dockerfile.goreleaser`) and local (`./Dockerfile`) images. obs_exporter's
local `./Dockerfile` is rewritten to match its already-Alpine
`Dockerfile.goreleaser`: named user `obs`, uid `10001` (was
`nonroot:nonroot`/`65532`), `HEALTHCHECK`/`healthcheck:` against `/livez` via
`127.0.0.1` (never `localhost` — Alpine's busybox `wget` resolves `localhost`
via `::1` first, and the exporter only binds IPv4).

## Consequences

- Non-breaking for obs_exporter's published image (already Alpine, already uid
  10001) — this ADR only fixes the local dev image and adds `HEALTHCHECK`.
  pmax_exporter, ppdd_exporter, and ppdm_exporter's own ADRs record the
  breaking UID change on their published images.
- The full family standard and per-repo work breakdown live in
  `docs/superpowers/specs/2026-08-01-alpine-standard-design.md`.
