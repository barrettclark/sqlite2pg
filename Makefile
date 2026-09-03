# sqlite2pg developer tasks. `make` or `make help` lists them.

GO  ?= go
PKG ?= ./...

# Pinned, not @latest: a new upstream release shouldn't change results or
# break CI with no repo change (the vuln DB govulncheck queries stays live
# regardless). Bump deliberately; CI pins the same versions.
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
GOVULNCHECK   ?= go run golang.org/x/vuln/cmd/govulncheck@v1.7.0

# `make campaign` inputs (see scripts/verify-all-fixtures.sh for the rest).
PG_URL        ?= postgres://localhost:5432/?sslmode=disable
MORE_DATA_DIR ?= $(CURDIR)/../more data
BEETS_DB      ?= $(HOME)/Downloads/beets_library.db

.DEFAULT_GOAL := help
.PHONY: help build test test-integration vet lint lint-fix fmt fmt-check tidy-check vulncheck cover check campaign release-check clean

help: ## List targets
	@grep -hE '^[a-z][a-z-]*:.*## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*## "}{printf "  \033[1m%-16s\033[0m %s\n", $$1, $$2}'

build: ## Compile everything
	$(GO) build $(PKG)

test: ## Unit tests (tiers 1-2, no Postgres)
	$(GO) test $(PKG)

test-integration: ## Tier-3 tests against a real Postgres (export PGURL to override the localhost default)
	$(GO) test -tags integration $(PKG)

vet: ## go vet
	$(GO) vet $(PKG)

lint: ## golangci-lint (bundles staticcheck, errcheck, govet, ...)
	$(GOLANGCI_LINT) run $(PKG)

lint-fix: ## golangci-lint with --fix
	$(GOLANGCI_LINT) run --fix $(PKG)

fmt: ## Rewrite files with gofmt
	gofmt -w .

fmt-check: ## Fail if any file needs gofmt (or can't be parsed)
	@out=$$(gofmt -l -e . 2>&1); if [ -n "$$out" ]; then printf 'gofmt:\n%s\n' "$$out"; exit 1; fi

tidy-check: ## Fail if go.mod/go.sum aren't tidy
	$(GO) mod tidy
	git diff --exit-code go.mod go.sum

vulncheck: ## Scan deps + reachable code for known CVEs
	$(GOVULNCHECK) $(PKG)

cover: ## Unit-test coverage; opens the HTML report
	$(GO) test -coverprofile=coverage.out $(PKG)
	$(GO) tool cover -html=coverage.out

check: fmt-check lint tidy-check vulncheck test ## Everything CI runs

campaign: build ## Full load-test campaign over every local SQLite fixture (needs Postgres + libpq tools)
	PG_URL="$(PG_URL)" MORE_DATA_DIR="$(MORE_DATA_DIR)" BEETS_DB="$(BEETS_DB)" \
		scripts/verify-all-fixtures.sh

release-check: fmt-check ## Validate the goreleaser config
	goreleaser check

clean: ## Remove build output
	rm -rf bin dist coverage.out
