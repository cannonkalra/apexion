# Apexion — build & developer automation.
#
#   make            show this help
#   make run        run the server locally (needs an S3 store on :9000)
#   make build      build ./bin/apexion for the host platform
#   make up         full local stack in Docker (MinIO + seed + app)
#
# The generated *_templ.go files and assets/css/app.css are committed, so a
# plain `go build ./cmd/apexion` works without the templ/tailwind toolchains.

# ---- configuration ---------------------------------------------------------

BIN_DIR   := bin
DIST_DIR  := dist
APP       := apexion
PKG       := ./cmd/apexion
SEED_PKG  := ./cmd/seed

# Version metadata. Derived from git and injected via -ldflags. The build date
# is the commit date (not the wall clock) so builds are reproducible.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell git show -s --format=%cI HEAD 2>/dev/null || echo unknown)

MODULE  := github.com/apexion/apexion
LDFLAGS := -s -w \
  -X main.version=$(VERSION) \
  -X main.commit=$(COMMIT) \
  -X main.date=$(DATE)

# CGO is required by the embedded DuckDB driver.
export CGO_ENABLED := 1

GOOS   ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
EXT    := $(if $(filter windows,$(GOOS)),.exe,)

# Tailwind standalone binary (only needed to recompile CSS).
TEMPL    := go tool templ
TAILWIND := $(BIN_DIR)/tailwindcss
UNAME_S  := $(shell uname -s | tr '[:upper:]' '[:lower:]')
UNAME_M  := $(shell uname -m)
TW_OS    := $(if $(filter darwin,$(UNAME_S)),macos,linux)
TW_ARCH  := $(if $(filter arm64 aarch64,$(UNAME_M)),arm64,x64)
TW_URL   := https://github.com/tailwindlabs/tailwindcss/releases/download/v3.4.17/tailwindcss-$(TW_OS)-$(TW_ARCH)

.DEFAULT_GOAL := help

# ---- help ------------------------------------------------------------------

.PHONY: help
help: ## Show this help
	@echo "Apexion — make targets:"
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# ---- quality ---------------------------------------------------------------

.PHONY: fmt
fmt: ## Format Go code
	go fmt ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint (install: https://golangci-lint.run)
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not found — see https://golangci-lint.run/welcome/install/"; exit 1; }
	golangci-lint run

.PHONY: test
test: ## Run tests
	go test ./...

.PHONY: race
race: ## Run tests with the race detector
	go test -race ./...

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	go mod tidy

.PHONY: check
check: fmt vet test ## fmt + vet + test

# ---- codegen ---------------------------------------------------------------

.PHONY: generate
generate: ## Regenerate Go from .templ files
	$(TEMPL) generate

.PHONY: css
css: $(TAILWIND) ## Recompile Tailwind CSS
	$(TAILWIND) -c tailwind.config.js -i assets/css/input.css -o assets/css/app.css --minify

.PHONY: assets
assets: generate css ## Regenerate templ + CSS

$(TAILWIND):
	@mkdir -p $(BIN_DIR)
	@echo "downloading tailwindcss ($(TW_OS)-$(TW_ARCH))…"
	@curl -fsSL -o $(TAILWIND) $(TW_URL)
	@chmod +x $(TAILWIND)

# ---- build & run -----------------------------------------------------------

.PHONY: build
build: ## Build ./bin/apexion for the host platform
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(APP) $(PKG)

.PHONY: build-seed
build-seed: ## Build the sample-data seeder into ./bin
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/seed $(SEED_PKG)

.PHONY: install
install: ## Install apexion into $GOPATH/bin
	go install -trimpath -ldflags '$(LDFLAGS)' $(PKG)

.PHONY: run
run: ## Run the server locally (needs an S3 store on :9000)
	go run -ldflags '$(LDFLAGS)' $(PKG) serve -c configs/apexion.yaml

.PHONY: seed
seed: ## Upload sample datasets to the local store
	go run $(SEED_PKG)

.PHONY: crawl
crawl: ## Crawl a bucket, e.g. make crawl BUCKET=warehouse
	go run $(PKG) crawl $(or $(BUCKET),warehouse) -c configs/apexion.yaml

# ---- release ---------------------------------------------------------------
# Because the DuckDB driver uses CGO, each target platform is built natively on
# its own runner. `dist` builds the current GOOS/GOARCH; CI (.github/workflows/
# release.yml) fans this out across linux/amd64, linux/arm64, darwin/arm64, and
# windows/amd64, then aggregates the checksums.

.PHONY: dist
dist: ## Build a release binary for the host platform into ./dist
	@mkdir -p $(DIST_DIR)
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST_DIR)/$(APP)-$(GOOS)-$(GOARCH)$(EXT) $(PKG)
	@echo "→ $(DIST_DIR)/$(APP)-$(GOOS)-$(GOARCH)$(EXT)"

.PHONY: checksums
checksums: ## Generate dist/SHA256SUMS from the built binaries
	@cd $(DIST_DIR) && shasum -a 256 $(APP)-* > SHA256SUMS && cat SHA256SUMS

.PHONY: release
release: dist checksums ## Build the host-platform release binary + checksums
	@echo "release $(VERSION) staged in $(DIST_DIR)/"

.PHONY: snapshot
snapshot: ## Build an untagged snapshot release for the host platform
	@$(MAKE) release VERSION=$(VERSION)-snapshot

.PHONY: build-linux
build-linux: ## Cross-build linux/amd64 into ./dist via Docker
	@mkdir -p $(DIST_DIR)
	docker run --rm --platform linux/amd64 \
	  -v "$(CURDIR)":/src -w /src \
	  -e CGO_ENABLED=1 \
	  golang:1.26-bookworm \
	  go build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST_DIR)/$(APP)-linux-amd64 $(PKG)
	@echo "→ $(DIST_DIR)/$(APP)-linux-amd64"

# ---- docker ----------------------------------------------------------------

.PHONY: up
up: ## Full local stack in Docker (MinIO + seed + app) → http://localhost:8080
	docker compose up --build

.PHONY: down
down: ## Stop and remove the Docker stack
	docker compose down -v

.PHONY: docker-build
docker-build: ## Build the Docker image
	docker build -t apexion:latest .

# ---- local SeaweedFS ---------------------------------------------------------
# An S3 store for running the app natively (make run / ./bin/apexion serve).

SEAWEED_COMPOSE := docker compose -f docker-compose.seaweed.yml
SEAWEED_CONN    := {"name":"seaweedfs-local","provider":"seaweed","endpoint":"localhost:8333","region":"us-east-1","access_key":"admin","secret_key":"password","path_style":true}

.PHONY: seaweed
seaweed: ## Start local SeaweedFS (S3 on :8333) and seed the warehouse bucket
	$(SEAWEED_COMPOSE) up -d
	@until curl -s -o /dev/null localhost:8333/; do sleep 1; done
	APEXION_MINIO_ENDPOINT=localhost:8333 APEXION_MINIO_ACCESS_KEY=admin \
	  APEXION_MINIO_SECRET_KEY=password go run $(SEED_PKG)

.PHONY: seaweed-connect
seaweed-connect: ## Add + activate the SeaweedFS connection in a running app on :8080
	@id=$$(curl -sf -XPOST localhost:8080/api/connections -H 'content-type: application/json' \
	  -d '$(SEAWEED_CONN)' | sed -E 's/.*"id":"([^"]+)".*/\1/') && \
	  curl -sf -XPOST localhost:8080/api/connections/$$id/activate >/dev/null && \
	  echo "→ seaweedfs-local is the active connection"

.PHONY: seaweed-down
seaweed-down: ## Stop local SeaweedFS and delete its data
	$(SEAWEED_COMPOSE) down -v

# ---- housekeeping ----------------------------------------------------------

.PHONY: clean
clean: ## Remove build artifacts and the local catalog
	rm -rf $(BIN_DIR) $(DIST_DIR) data/*.duckdb
