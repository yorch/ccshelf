# ccshelf developer tasks. Needs Go, make and bash; python3 for the docs targets.
# Run `make help` for the list.

SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

MODULE   := github.com/ccshelf/ccshelf
CMD      := ./cmd/ccshelf
DIST     := dist
VERSION  ?= dev
COMMIT   ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo none)
DATE     ?= $(shell git log -1 --format=%cs 2>/dev/null || date -u +%Y-%m-%d)
LDFLAGS  := -s -w \
  -X $(MODULE)/internal/version.Version=$(VERSION) \
  -X $(MODULE)/internal/version.Commit=$(COMMIT) \
  -X $(MODULE)/internal/version.Date=$(DATE)
GOEXE    := $(shell go env GOEXE)
BIN      := $(DIST)/ccshelf$(GOEXE)
COVER_MIN ?= 70

# Pinned versions of the development tools (keep in step with .github/workflows/ci.yml).
GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION   := v1.8.0
GORELEASER_VERSION    := v2.18.2
ACTIONLINT_VERSION    := v1.7.12

TARGETS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

.PHONY: help build test test-race cover lint fmt fmt-check vet vuln cross docs docs-check \
        examples-check e2e action-test pins-check links-check eol-check snapshot tools clean ci

help: ## List the targets
	@awk 'BEGIN { FS = ":.*## " } /^[a-zA-Z_-]+:.*## / { printf "  %-16s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

build: ## Build the ccshelf binary into dist/
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) $(CMD)

test: ## Run the unit tests
	go test -count=1 ./...

test-race: ## Run the unit tests with the race detector
	go test -race -count=1 ./...

cover: ## Coverage: HTML report in dist/coverage.html and a threshold check (COVER_MIN, default 70)
	@mkdir -p $(DIST)
	go test -count=1 -covermode=atomic -coverprofile=$(DIST)/coverage.out ./...
	go tool cover -html=$(DIST)/coverage.out -o $(DIST)/coverage.html
	scripts/check-cover.sh $(DIST)/coverage.out $(COVER_MIN)

lint: ## Run golangci-lint (install with `make tools`)
	golangci-lint run ./...

fmt: ## Format the code (gofumpt through golangci-lint)
	golangci-lint fmt ./...

fmt-check: ## Fail if any file is not gofmt-clean
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "needs gofmt:"; echo "$$out"; exit 1; fi

vet: ## Run go vet
	go vet ./...

vuln: ## Run govulncheck (install with `make tools`)
	govulncheck ./...

cross: ## Cross-compile all six targets into dist/
	@mkdir -p $(DIST)
	@for t in $(TARGETS); do \
	  os=$${t%/*}; arch=$${t#*/}; ext=""; [ "$$os" = windows ] && ext=".exe"; \
	  echo "build $$os/$$arch"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
	    -o "$(DIST)/ccshelf-$$os-$$arch$$ext" $(CMD); \
	done

docs: ## Rebuild docs/report.html from the Markdown
	python3 docs/build_report.py

docs-check: ## Fail if docs/report.html is out of date
	python3 docs/build_report.py --check

examples-check: build ## Run lint, compile --check and catalog build on examples/org-data-repo
	scripts/check-examples.sh $(BIN)

e2e: ## Run the end-to-end tests of the built binary against a fake claude
	go test -race -count=1 ./internal/e2e/...

action-test: ## Run the offline tests of the Action installer
	bash action/test/run.sh

pins-check: ## Fail if a workflow `uses:` is not pinned by full SHA
	scripts/check-pins.sh

links-check: ## Check relative Markdown links
	scripts/check-links.sh

eol-check: ## Check LF endings and final newlines
	scripts/check-eol.sh

snapshot: ## Build a local goreleaser snapshot (no signing, no publishing)
	goreleaser release --snapshot --clean --skip=sign,sbom

tools: ## Install the pinned development tools into $(GOPATH)/bin
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)
	go install github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

clean: ## Remove build output and coverage files
	rm -rf $(DIST) coverage.out coverage.html

ci: fmt-check vet lint test-race vuln pins-check action-test docs-check links-check eol-check examples-check ## What CI runs
	@command -v actionlint >/dev/null 2>&1 && actionlint || echo "actionlint not installed; skipped (make tools)"
