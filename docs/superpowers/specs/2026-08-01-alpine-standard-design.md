# Alpine as the sole container-image standard — design

**Date:** 2026-08-01
**Status:** approved, not implemented
**Scope:** family-wide — obs_exporter, nbu_exporter, pflex_exporter, pmax_exporter,
ppdd_exporter, ppdm_exporter, pscale_exporter, pstore_exporter

## Problem

The `exporter-standards` skill (`references/stack.md`) framed the family's container
base image as "canonical: Alpine, `ppdd`'s distroless is an accepted stricter
variant" — written when only one repo had gone distroless. Verified against the
image each repo actually publishes (`Dockerfile.goreleaser`, not the local
`./Dockerfile`, which some repos leave to diverge):

| Published image | User | Shell | Repos |
|---|---|---|---|
| Alpine | named (`obs`, `nbu`, …), uid 10001 | present | obs, nbu, pflex, pscale, pstore |
| `gcr.io/distroless/static:nonroot` | `nonroot:nonroot`, uid 65532 | absent | pmax, ppdd, ppdm |

Confirmed by pulling and inspecting each repo's latest published tag directly — not
by reading Dockerfile text alone. 5 repos publish Alpine, 3 publish distroless. The
split is real, per-repo author choice, with no written criterion anywhere in the
skill and no chronological pattern (interleaved by date, not "old vs new").
`obs_exporter` additionally has an internal inconsistency: its local `./Dockerfile`
is distroless while its published `Dockerfile.goreleaser` is Alpine — the two
diverge within the same repo.

Alpine has a shell and `wget`, so it can carry a Docker `HEALTHCHECK` and a Compose
`healthcheck:` block pointed at `/livez` (added and verified on `nbu`/`pflex`/
`pscale`/`pstore`'s local `./Dockerfile` and compose files 2026-08-01, but not yet
on any repo's `Dockerfile.goreleaser` — the file that actually ships). Distroless
cannot: no shell, no HTTP client inside the image, health can only be probed from
outside (Kubernetes probes, an external Compose-level check).

## Decision

**Alpine is the sole standard, family-wide, for both the published
(`Dockerfile.goreleaser`) and local (`./Dockerfile`) images.** The three distroless
repos (`pmax`, `ppdd`, `ppdm`) convert. `obs_exporter`'s local `./Dockerfile` is
brought in line with its already-Alpine `Dockerfile.goreleaser`.

This is accepted as a breaking change for `pmax`/`ppdd`/`ppdm`'s published image
(container UID moves from `65532` to a named-user `10001`, matching the rest of the
family) in exchange for consistency, in-image `HEALTHCHECK`, and shell-based
debuggability — the trade against a smaller distroless attack surface was
considered and explicitly accepted for the whole family, not decided per repo.

### Canonical `Dockerfile.goreleaser` (published image)

For repo `<name>_exporter`, port `<port>`:

```dockerfile
# Used by GoReleaser (dockers_v2). The binary is cross-compiled by the build pipe;
# buildx lays it out per-platform as ${TARGETPLATFORM}/<name>_exporter in the context.
# For local/dev builds from source, use the multi-stage ./Dockerfile instead.
FROM alpine:latest

RUN apk --no-cache add ca-certificates && \
    adduser -D -u 10001 <name> && \
    mkdir -p /var/log/<name>_exporter && \
    chown <name>:<name> /var/log/<name>_exporter

ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/<name>_exporter /usr/bin/<name>_exporter
COPY config.yaml /etc/<name>_exporter/config.yaml

EXPOSE <port>

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --quiet --tries=1 --spider http://127.0.0.1:<port>/livez || exit 1

USER <name>

ENTRYPOINT ["/usr/bin/<name>_exporter"]
CMD ["--config", "/etc/<name>_exporter/config.yaml"]
```

`apk add ca-certificates` fetches directly from the Alpine CDN — tolerated here
because CI has open egress (unlike the local dev image, below).

### Canonical local `./Dockerfile`

Same runtime stage, but built from source with a `golang` builder stage, and CA
certs copied from the builder instead of `apk add`ed — the bare Alpine image has no
CA bundle yet to validate a corporate MITM proxy's certificate, and `apk add`
itself needs TLS to reach the Alpine CDN (chicken-and-egg):

```dockerfile
# Stage 1: Build
FROM golang:1.26.5 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o <name>_exporter .

# Stage 2: Runtime
FROM alpine:latest

RUN adduser -D -u 10001 <name> && \
    mkdir -p /var/log/<name>_exporter && \
    chown <name>:<name> /var/log/<name>_exporter

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /app/<name>_exporter /usr/bin/<name>_exporter
COPY config.yaml /etc/<name>_exporter/config.yaml

EXPOSE <port>

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --quiet --tries=1 --spider http://127.0.0.1:<port>/livez || exit 1

USER <name>

ENTRYPOINT ["/usr/bin/<name>_exporter"]
CMD ["--config", "/etc/<name>_exporter/config.yaml"]
```

### Canonical Compose `healthcheck:`

Added to the exporter service block in every `docker-compose.yml` and
`docker-compose.ghcr.yml` (and any per-repo compose variant, e.g. `ppdd`'s
`docker-compose.server.yml`):

```yaml
healthcheck:
  test: ["CMD", "wget", "--quiet", "--tries=1", "--spider", "http://127.0.0.1:<port>/livez"]
  interval: 30s
  timeout: 10s
  retries: 3
  start_period: 10s
```

`127.0.0.1`, never `localhost`: Alpine's busybox `wget` resolves `localhost` via
`::1` first, and these exporters only bind IPv4 — a `localhost`-based check always
fails with connection refused. Confirmed by building and running an image, not
just by reading the Dockerfile (caught this exact bug on `nbu_exporter`
2026-08-01).

Compose's `healthcheck:`, when present, is what `depends_on: condition:
service_healthy` uses and takes precedence over the image's own `HEALTHCHECK` for
containers launched via Compose. The image's `HEALTHCHECK` still matters
independently for plain `docker run` and for `docker ps`/`docker inspect`
readability, and Kubernetes ignores both (it uses its own liveness/readiness
probes) — the three are not redundant, each serves a different launch path.

## Per-repo work

| Repo | Local `./Dockerfile` | `Dockerfile.goreleaser` | Compose |
|---|---|---|---|
| `obs_exporter` | rewrite (distroless → Alpine) | add `HEALTHCHECK` | add `healthcheck:` to `.yml` + `.ghcr.yml` (currently absent) |
| `pmax_exporter` | rewrite | rewrite (distroless → Alpine) | add `healthcheck:` to `.yml`; **create** `.ghcr.yml` (pre-existing drift — this repo is the only one missing it) |
| `ppdd_exporter` | rewrite | rewrite | add `healthcheck:` to `.yml`, `.ghcr.yml`, and `.server.yml` (all three define the `ppdd_exporter` service) |
| `ppdm_exporter` | rewrite | rewrite | add `healthcheck:` to `.yml`; **create** `.ghcr.yml` (same pre-existing drift) |
| `nbu_exporter`, `pflex_exporter`, `pscale_exporter`, `pstore_exporter` | already done (2026-08-01) | add `HEALTHCHECK` | already done (2026-08-01) |

`pmax`/`ppdd`/`ppdm` already have `/livez`/`/readyz` wired in Go (confirmed) — no
application code changes needed, this is Docker/Compose-only.

Helm charts checked for a hardcoded distroless UID (`runAsUser: 65532` or
similar): none found — `pmax`/`ppdd`/`ppdm`'s `values.yaml` only carry the generic
commented-out Helm scaffold default (`# runAsUser: 1000`), never active. No chart
changes required.

## Documentation

**One ADR per repo**, next available number in that repo's own sequence, recording
the decision (context table above, decision, consequences — UID change is breaking
for `pmax`/`ppdd`/`ppdm` only, no chart impact, deliberate attack-surface trade
accepted family-wide).

**CHANGELOG**:
- `pmax`, `ppdd`, `ppdm`: `Breaking` entry — published image base changes from
  `gcr.io/distroless/static:nonroot` to `alpine:latest`; container UID changes
  from `65532` to a named user at `10001`; adds `HEALTHCHECK`.
- `obs`, `nbu`, `pflex`, `pscale`, `pstore`: `Added` entry — `HEALTHCHECK` added to
  the published image (non-breaking, additive). `obs` additionally notes its local
  dev image (`make docker`) now matches its published image (previously diverged).

**`exporter-standards` skill** (this repo does not own it; edited directly under
`~/.claude/skills/exporter-standards/`):
- `references/stack.md`: replace the stale "canonical Alpine, `ppdd`'s distroless
  is an accepted variant" line with "Alpine is the sole standard, family-wide,
  both published and local images" and the canonical Dockerfile/HEALTHCHECK
  content above.
- `references/new-exporter-checklist.md`: add the `HEALTHCHECK`/uid-10001
  requirement to the Dockerfile checklist item for new exporters.
- `references/decisions.md`: record the correction as drift-fixed, dated
  2026-08-01, replacing any stale distroless-variant framing.

## Testing

Per repo: `hadolint` on both Dockerfiles, `docker compose config -q` on every
compose file touched, and one real build-and-run verification (as done for
`nbu_exporter`) — build the image, run it with the env vars its compose file
defaults to, and confirm `docker inspect --format='{{.State.Health.Status}}'`
reports `healthy`, not just that the file parses. `go test ./...` and the repo's
own `make ci`/`make sure` gate must stay green (no Go code changes expected, but
the gate is the source of truth).

## Out of scope

- Any change to `/health`, `/livez`, `/readyz` application behavior — separate,
  already-landed family effort (ADR-0013 lineage).
- Helm chart `securityContext` defaults — confirmed unaffected, not touched.
- Migrating any repo *away* from Alpine — this is a one-directional, one-time
  correction of undocumented drift, not a recurring choice.
