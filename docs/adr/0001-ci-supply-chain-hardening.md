# CI/CD supply-chain hardening

## Status

Accepted (2026-06-10)

## Context

v1 used Travis CI with unpinned tooling and a hand-rolled release flow. The
exporter family standard requires reproducible releases and a hardened pipeline
(see the sibling ADRs: pflex 0001, ppdd 0008, pstore 0008).

## Decision

- **Workflow trio**: `ci.yml` (make ci + CycloneDX SBOM artifact + Semgrep),
  `release.yml` (GoReleaser + multi-arch GHCR image with SBOM/provenance
  attestations), `docs.yml` (MkDocs Material → GitHub Pages).
- **Every GitHub Action is pinned to a full commit SHA** with an explicit
  `# vX.Y.Z` comment; `.github/dependabot.yml` bumps SHA + comment together (plus
  gomod and docker ecosystems).
- **GoReleaser** (`.goreleaser.yaml`, v2) is the single source of truth for
  releases: CGO off, linux/darwin × amd64/arm64, `-trimpath`, commit-timestamped
  builds, sha256 checksums, module-level CycloneDX SBOM via `cyclonedx-gomod`, and
  a Homebrew cask that self-skips while the tap PAT is absent.
- **Everything CI runs is a Makefile target** (`make ci` = fmt-check, vet,
  golangci-lint, `go test -race`, govulncheck, build) so failures reproduce locally.
- Container images run as a **non-root user** on `distroless/static:nonroot`.
- No inline `nosemgrep`/`nolint` suppressions — restructure code instead.

## Consequences

- A compromised action tag cannot silently change our pipeline; updates arrive as
  reviewable Dependabot PRs.
- Releases are reproducible and carry SBOM + provenance for downstream auditing.
- Until the repo's default branch is renamed `main`, workflows trigger on both
  `main` and `master`.
