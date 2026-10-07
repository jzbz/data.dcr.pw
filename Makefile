# data.dcr.pw — common developer tasks.
#
# This is a multi-module Go repo. The web explorer binary lives in cmd/dcrdata
# and serves its assets (public/, views_v2/) relative to its working directory,
# so it is built and run from there. The front end is plain CSS + native ES
# modules — there is no Node.js/npm/bundler step.

GO      ?= go
APP_DIR := cmd/dcrdata
BIN     := dcrdata
GOFLAGS ?=

# Go modules in this repo, derived from the tracked go.mod files so none are
# missed (pubsub has no module of its own — pubsub/democlient does). db/dcrpg's
# tests need a live PostgreSQL, so the default `test` target covers just the
# app and core libraries; use `test-all` to run every module.
MODULES := $(sort $(dir $(shell git ls-files 'go.mod' '*/go.mod')))

.DEFAULT_GOAL := help

.PHONY: help build run test test-all vet lint fmt tidy clean

help: ## Show this help.
	@awk 'BEGIN{FS=":.*## "} /^[a-zA-Z_-]+:.*## /{printf "  \033[36m%-10s\033[0m %s\n",$$1,$$2}' $(MAKEFILE_LIST)

build: ## Build the dcrdata binary into cmd/dcrdata/.
	cd $(APP_DIR) && $(GO) build $(GOFLAGS) -o $(BIN) .

run: build ## Build and run dcrdata (serves assets from cmd/dcrdata).
	cd $(APP_DIR) && ./$(BIN)

test: ## Run unit tests for the web app and core libraries.
	$(GO) test ./...
	cd $(APP_DIR) && $(GO) test ./...

test-all: ## Run tests across every module (db/dcrpg needs PostgreSQL).
	@set -e; for m in $(MODULES); do echo "== test $$m =="; ( cd $$m && $(GO) test ./... ); done

vet: ## Run go vet on the web app and core libraries.
	$(GO) vet ./...
	cd $(APP_DIR) && $(GO) vet ./...

lint: ## Run golangci-lint (config: .golangci.yml).
	golangci-lint run ./...
	cd $(APP_DIR) && golangci-lint run ./...

fmt: ## gofmt all tracked Go sources in place.
	gofmt -w $$(git ls-files '*.go')

tidy: ## go mod tidy across every module.
	@set -e; for m in $(MODULES); do echo "== tidy $$m =="; ( cd $$m && $(GO) mod tidy ); done

clean: ## Remove build artifacts.
	rm -f $(APP_DIR)/$(BIN)
