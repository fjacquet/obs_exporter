BIN  := bin/obs_exporter
DIST ?= dist
COVER ?= coverage.out
IMAGE ?= obs_exporter:dev
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Pinned tool versions (installed by `make tools`).
GOLANGCI_VERSION    ?= v2.12.2
GORELEASER_VERSION  ?= v2.16.0
CYCLONEDX_GOMOD_VERSION ?= latest
GOVULNCHECK_VERSION     ?= latest

.PHONY: all clean install tools tools-sbom fmt fmt-check format vet lint \
        test test-race test-coverage build vuln sbom security docs \
        coverage-upload release release-snapshot ci sure \
        cli docker run-cli demo demo-ghcr demo-down clean-dist

.DEFAULT_GOAL := all

all: clean lint test build

# --- tooling ---

# Install pinned dev/CI tooling into $(GOBIN)/$GOPATH/bin.
tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@$(CYCLONEDX_GOMOD_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)

# Just the SBOM generator — used by the release pipeline (GoReleaser sboms hook).
tools-sbom:
	go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@$(CYCLONEDX_GOMOD_VERSION)

# --- quality gates ---

fmt:
	gofmt -w .
fmt-check:
	@test -z "$$(gofmt -l . | tee /dev/stderr)"
format:
	golangci-lint fmt
vet:
	go vet ./...
lint:
	golangci-lint run --timeout=5m
test:
	go test -race -coverprofile=$(COVER) -covermode=atomic ./...
test-race:
	go test -race -cover ./...
test-coverage:
	go test -coverprofile=$(COVER) ./...
	go tool cover -func=$(COVER) | tail -1
build:
	go build -v ./...
vuln:
	govulncheck ./...

# Local convenience gate.
sure: fmt-check vet test cli
# Aggregate gate run by CI: lint + test + build + vuln.
ci: lint test build vuln

# --- install ---

install:
	go mod download

# --- artifacts ---

cli:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) .

run-cli: cli
	$(BIN) --config config.yaml --debug

# CycloneDX SBOM for the Go module (source/dependency SBOM).
sbom:
	@mkdir -p $(DIST)
	cyclonedx-gomod mod -licenses -json -output $(DIST)/sbom.cdx.json
	@echo "wrote $(DIST)/sbom.cdx.json"

security:
	uvx semgrep scan --config auto --error --skip-unknown-extensions

docs:
	uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict --site-dir site

coverage-upload:
	uvx --from codecov-cli codecov upload-process --file $(COVER) || true

# Local/dev container image (the release image is built multi-arch in CI).
docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

# Cross-compiled binaries + archives + SBOM + checksums + GitHub Release.
release: tools-sbom
	goreleaser release --clean

# Local dry-run: full pipeline (build, archive, SBOM, checksums) without publishing.
release-snapshot: tools-sbom
	goreleaser release --snapshot --clean
	@echo "release artifacts in $(DIST)/"

# End-to-end demo stack (mockecs -> exporter -> Prometheus -> Grafana).
demo:
	docker compose up --build
demo-ghcr:
	docker compose -f docker-compose.ghcr.yml up
demo-down:
	docker compose down --remove-orphans
	docker compose -f docker-compose.ghcr.yml down --remove-orphans

clean-dist:
	rm -rf $(DIST)
clean: clean-dist
	rm -rf site $(COVER) *.sarif
	rm -f $(BIN)
