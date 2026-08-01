# Alpine Standard — obs_exporter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring obs_exporter's local `./Dockerfile` in line with its already-Alpine `Dockerfile.goreleaser` (currently the only repo in the family with local/published Dockerfile drift), and add `HEALTHCHECK`/`healthcheck:` everywhere it's still missing.

**Architecture:** `Dockerfile.goreleaser` is already `alpine:latest` (gains `HEALTHCHECK` only). `./Dockerfile` is currently `gcr.io/distroless/static:nonroot` and gets rewritten to match the family's canonical Alpine runtime stage, keeping its existing builder stage (Go version, ldflags, `VERSION` build arg) untouched. Both compose files get a `healthcheck:` block (neither has one today).

**Tech Stack:** Docker, Alpine (`wget`/busybox), Go 1.26.5.

**Spec:** `docs/superpowers/specs/2026-08-01-alpine-standard-design.md` (this repo).

## Global Constraints

- `HEALTHCHECK`/`healthcheck:` target `http://127.0.0.1:9438/livez`, never `localhost` — Alpine's busybox `wget` resolves `localhost` via `::1` first, and the exporter only binds IPv4.
- Timing: `--interval=30s --timeout=5s --start-period=10s --retries=3`.
- The local `./Dockerfile`'s builder stage (`FROM golang:1.26.5 AS build`, `WORKDIR /src`, `ARG VERSION=dev`, the exact `go build` line) does not change — only the runtime stage (final `FROM`) does.
- Uid `10001`, named user `obs` (matches the rest of the already-Alpine family; was `nonroot:nonroot`/`65532`).
- No inline `nosemgrep`/`//nolint` suppressions.
- `make ci` must stay green (no Go code changes expected).

## File Structure

| File | Responsibility |
| --- | --- |
| `Dockerfile` | Rewrite runtime stage: distroless → Alpine, add `HEALTHCHECK` |
| `Dockerfile.goreleaser` | Add `HEALTHCHECK` before `USER obs` |
| `docker-compose.yml`, `docker-compose.ghcr.yml` | Add `healthcheck:` to the `obs_exporter` service |
| `docs/adr/0016-alpine-standard.md` | Records the decision (number TBD — confirm before writing) |
| `CHANGELOG.md` | `Added` entries: HEALTHCHECK (both images), local dev image now matches published |

---

### Task 1: Rewrite the local ./Dockerfile to Alpine

**Files:**
- Modify: `Dockerfile`

**Interfaces:** none — single-file, no code.

- [ ] **Step 1: Replace the runtime stage**

Current file:

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.26.5 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/obs_exporter .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/obs_exporter /obs_exporter
USER nonroot:nonroot
EXPOSE 9438
ENTRYPOINT ["/obs_exporter"]
CMD ["--config", "/etc/obs_exporter/config.yaml"]
```

Replace with:

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.26.5 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/obs_exporter .

FROM alpine:latest

# Create the runtime user and log dir. These are busybox builtins (no network).
RUN adduser -D -u 10001 obs && \
    mkdir -p /var/log/obs_exporter && \
    chown obs:obs /var/log/obs_exporter

# Copy the CA bundle from the builder stage instead of `apk add ca-certificates`.
# The latter fetches from the Alpine CDN over TLS, which fails behind a corporate
# MITM proxy: the bare alpine image has no CA bundle yet to validate the proxy
# cert (chicken-and-egg). The Debian-based golang builder already ships the bundle.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

COPY --from=build /out/obs_exporter /usr/bin/obs_exporter
COPY config.yaml /etc/obs_exporter/config.yaml

EXPOSE 9438

# /livez never depends on target reachability or the collection cycle, so it
# can never flag a healthy process as down over an unreachable ObjectScale
# cluster (see ADR-0016 / exporter-standards references/architecture.md
# "Health probes").
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --quiet --tries=1 --spider http://127.0.0.1:9438/livez || exit 1

USER obs

ENTRYPOINT ["/usr/bin/obs_exporter"]
CMD ["--config", "/etc/obs_exporter/config.yaml"]
```

Note the binary moves from `/obs_exporter` to `/usr/bin/obs_exporter` (matches the rest of the family), and the local image now bakes in `config.yaml` — it didn't before (distroless local Dockerfiles across the family skip this; Alpine ones don't). This is an intentional, additive standardization, not a bug fix.

- [ ] **Step 2: Lint**

Run: `hadolint Dockerfile`
Expected: no findings that reference the `HEALTHCHECK`/`adduser`/`COPY` lines just added. Pre-existing findings on lines this diff didn't touch are out of scope.

- [ ] **Step 3: Build and verify at runtime**

```bash
docker build -t obs_exporter:alpine-test .
docker run -d --name obs-hc-test -p 19438:9438 \
  -v "$(pwd)/config.demo.yaml:/etc/obs_exporter/config.yaml:ro" \
  obs_exporter:alpine-test
sleep 15
docker inspect --format='{{.State.Health.Status}}' obs-hc-test
docker exec obs-hc-test whoami
```

Expected: `healthy`, and `whoami` prints `obs` (confirms the `USER obs` / uid 10001 switch took effect, not a leftover distroless artifact).

- [ ] **Step 4: Clean up test artifacts**

```bash
docker rm -f obs-hc-test
docker rmi obs_exporter:alpine-test
```

- [ ] **Step 5: Commit**

```bash
git add Dockerfile
git commit -m "feat(docker): rewrite local Dockerfile to Alpine, matching the published image"
```

---

### Task 2: Add HEALTHCHECK to Dockerfile.goreleaser

**Files:**
- Modify: `Dockerfile.goreleaser`

**Interfaces:** none.

- [ ] **Step 1: Edit the file**

Insert before `USER obs`:

```dockerfile
EXPOSE 9438

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --quiet --tries=1 --spider http://127.0.0.1:9438/livez || exit 1

USER obs
```

(`EXPOSE 9438` already exists — only the `HEALTHCHECK` block is new.)

- [ ] **Step 2: Lint**

Run: `hadolint Dockerfile.goreleaser`
Expected: no new findings.

- [ ] **Step 3: Build and verify at runtime**

```bash
CGO_ENABLED=0 go build -o obs_exporter .
mkdir -p linux/amd64 && cp obs_exporter linux/amd64/obs_exporter
docker build -f Dockerfile.goreleaser --build-arg TARGETPLATFORM=linux/amd64 -t obs_exporter:goreleaser-test .
docker run -d --name obs-gr-hc-test -p 19439:9438 \
  -v "$(pwd)/config.demo.yaml:/etc/obs_exporter/config.yaml:ro" \
  obs_exporter:goreleaser-test
sleep 15
docker inspect --format='{{.State.Health.Status}}' obs-gr-hc-test
```

Expected: `healthy`.

- [ ] **Step 4: Clean up test artifacts**

```bash
docker rm -f obs-gr-hc-test
docker rmi obs_exporter:goreleaser-test
rm -rf linux obs_exporter
```

- [ ] **Step 5: Commit**

```bash
git add Dockerfile.goreleaser
git commit -m "feat(docker): add HEALTHCHECK to the published image (Dockerfile.goreleaser)"
```

---

### Task 3: Add healthcheck: to both compose files

**Files:**
- Modify: `docker-compose.yml`
- Modify: `docker-compose.ghcr.yml`

**Interfaces:** none.

- [ ] **Step 1: Edit docker-compose.yml**

In the `obs_exporter` service, after `restart: unless-stopped` (line 32):

```yaml
    depends_on:
      - mockecs
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "--quiet", "--tries=1", "--spider", "http://127.0.0.1:9438/livez"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s
```

- [ ] **Step 2: Edit docker-compose.ghcr.yml**

Same block, in the `obs_exporter` service after its `restart: unless-stopped` (line 27):

```yaml
    depends_on:
      - mockecs
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "--quiet", "--tries=1", "--spider", "http://127.0.0.1:9438/livez"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s
```

- [ ] **Step 3: Validate**

Run: `docker compose -f docker-compose.yml config -q && docker compose -f docker-compose.ghcr.yml config -q`
Expected: both exit 0.

- [ ] **Step 4: Smoke-test via the real demo stack**

```bash
make demo
sleep 20
docker inspect --format='{{.State.Health.Status}}' obs_exporter
```

Expected: `healthy`. Tear down afterward (`docker compose down` or the demo's own teardown target).

- [ ] **Step 5: Commit**

```bash
git add docker-compose.yml docker-compose.ghcr.yml
git commit -m "feat(docker): add healthcheck to the demo compose stacks"
```

---

### Task 4: ADR + CHANGELOG

**Files:**
- Create: `docs/adr/0016-alpine-standard.md` (confirm this number is free before writing — `ls docs/adr/ | sort -V | tail -3`; this repo's most recent is ADR-0015)
- Modify: `docs/adr/index.md`
- Modify: `CHANGELOG.md`

**Interfaces:** none.

- [ ] **Step 1: Confirm the ADR number**

Run: `ls docs/adr/ | sort -V | tail -3`
Expected: `0015-health-always-200.md` is the latest; use `0016`.

- [ ] **Step 2: Write the ADR**

Create `docs/adr/0016-alpine-standard.md`:

```markdown
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
```

- [ ] **Step 3: Add the index.md row**

In `docs/adr/index.md`, following the existing row format, add a row for ADR-0016 summarizing "Alpine as the sole container-image standard, family-wide; obs_exporter's local Dockerfile fixed to match its published image."

- [ ] **Step 4: Add the CHANGELOG entries**

Under `## [Unreleased]`:

```markdown
### Added

- `HEALTHCHECK` added to both the published and local Docker images, checking
  `/livez`. The demo Compose stacks (`docker-compose.yml`,
  `docker-compose.ghcr.yml`) gain a matching `healthcheck:`. See ADR-0016.

### Fixed

- The local dev Docker image (`make docker`, `./Dockerfile`) now matches the
  published image: Alpine instead of distroless. Previously the two diverged
  within this repo. See ADR-0016.
```

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0016-alpine-standard.md docs/adr/index.md CHANGELOG.md
git commit -m "docs: record ADR-0016 (Alpine as the sole container-image standard)"
```

---

### Task 5: Full gate

- [ ] **Step 1: Run the CI gate**

Run: `make ci`
Expected: golangci-lint clean, `go test -race ./...` green (no Go changes, but confirms nothing else regressed), build succeeds, govulncheck clean.

- [ ] **Step 2: Commit any fixes**

```bash
git commit -am "fix: address CI gate findings for the Alpine standard change"
```

(Skip if the gate was clean — expected, since this task touches no Go code.)

## Self-Review

- Spec coverage: obs_exporter's row in the family table (local Dockerfile rewrite, goreleaser HEALTHCHECK, compose healthcheck on both files, currently absent) — Tasks 1–3. Documentation — Task 4.
- No placeholders: ADR number confirmed by a one-command check (Task 4, Step 1) rather than assumed.
- Scope: single repo; matches the family plan's per-repo row exactly. The three distroless repos' own conversions are separate plans in their own repos.
