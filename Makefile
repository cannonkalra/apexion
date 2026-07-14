# Apexion — build & run automation.
# Quick start:  make up      (full stack: MinIO + seed + app in Docker)
#               make run     (local dev server; needs MinIO on :9000)

SHELL := /bin/bash
BIN_DIR := bin
APP := $(BIN_DIR)/apexion
SEED := $(BIN_DIR)/seed
# templ is a Go tool dependency (see the `tool` directive in go.mod), so it is
# version-pinned and needs no separate install.
TEMPL := go tool templ
TAILWIND := $(BIN_DIR)/tailwindcss

# Detect platform for the Tailwind standalone binary.
UNAME_S := $(shell uname -s | tr '[:upper:]' '[:lower:]')
UNAME_M := $(shell uname -m)
ifeq ($(UNAME_S),darwin)
  TW_OS := macos
else
  TW_OS := linux
endif
ifeq ($(UNAME_M),arm64)
  TW_ARCH := arm64
else ifeq ($(UNAME_M),aarch64)
  TW_ARCH := arm64
else
  TW_ARCH := x64
endif
TW_URL := https://github.com/tailwindlabs/tailwindcss/releases/download/v3.4.17/tailwindcss-$(TW_OS)-$(TW_ARCH)

export CGO_ENABLED := 1

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

## ---- toolchain ----------------------------------------------------------

.PHONY: tools
tools: $(TAILWIND) ## Install the tailwind toolchain (templ runs via `go run`)

$(TAILWIND):
	@mkdir -p $(BIN_DIR)
	@echo "downloading tailwindcss ($(TW_OS)-$(TW_ARCH))…"
	@curl -fsSL -o $(TAILWIND) $(TW_URL)
	@chmod +x $(TAILWIND)

## ---- codegen ------------------------------------------------------------

.PHONY: generate
generate: ## Generate Go from .templ files
	$(TEMPL) generate

.PHONY: css
css: $(TAILWIND) ## Compile Tailwind CSS
	$(TAILWIND) -c tailwind.config.js -i assets/css/input.css -o assets/css/app.css --minify

.PHONY: assets
assets: generate css ## Regenerate templ + css

## ---- build & run --------------------------------------------------------

.PHONY: build
build: assets ## Build the apexion + seed binaries
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "-s -w" -o $(APP) ./cmd/apexion
	go build -trimpath -ldflags "-s -w" -o $(SEED) ./cmd/seed

.PHONY: run
run: assets ## Run the server locally (needs MinIO on :9000)
	go run ./cmd/apexion serve -c configs/apexion.yaml

.PHONY: seed
seed: ## Upload sample datasets to MinIO
	go run ./cmd/seed

.PHONY: crawl
crawl: ## Crawl a bucket, e.g. make crawl BUCKET=warehouse
	go run ./cmd/apexion crawl $(or $(BUCKET),warehouse) -c configs/apexion.yaml

## ---- docker -------------------------------------------------------------

.PHONY: up
up: ## Full stack in Docker (MinIO + seed + app) → http://localhost:8080
	docker compose up --build

.PHONY: down
down: ## Stop and remove the Docker stack
	docker compose down -v

.PHONY: docker-build
docker-build: ## Build the Docker image
	docker build -t apexion:latest .

## ---- quality ------------------------------------------------------------

.PHONY: test
test: ## Run unit + integration tests
	go test ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format Go code
	go fmt ./...

.PHONY: tidy
tidy: ## Tidy go.mod
	go mod tidy

.PHONY: clean
clean: ## Remove build artifacts and local catalog
	rm -rf $(BIN_DIR) data/*.duckdb
