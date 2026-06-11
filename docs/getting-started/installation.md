# Installation

## Binaries

Download the archive for your platform from
[GitHub Releases](https://github.com/fjacquet/obs_exporter/releases). Each release
ships `linux`/`darwin` × `amd64`/`arm64` binaries, SHA-256 checksums, and a
CycloneDX SBOM.

## Homebrew (macOS)

```bash
brew install --cask fjacquet/tap/obs_exporter
```

## Container image

Multi-arch images (with SBOM + provenance attestations) are published to GHCR on
every release:

```bash
docker run -v $PWD/config.yaml:/etc/obs_exporter/config.yaml:ro \
  -p 9438:9438 ghcr.io/fjacquet/obs_exporter:latest
```

## From source

Requires Go 1.26.4+:

```bash
git clone https://github.com/fjacquet/obs_exporter
cd obs_exporter
make cli          # builds bin/obs_exporter
```

## ECS prerequisites

- A management user with monitoring (read) rights on each cluster.
- Network access from the exporter host to the cluster's management port (4443).
- Only if you opt into `collectDT`: node-local ports 9101 and 9021 as well.
