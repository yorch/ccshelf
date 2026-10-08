# ccshelf developer tasks. Needs Go, just and bash; python3 for the docs recipes.
# Run `just` for the list. Override a variable with `just version=1.2.3 build`,
# or set it in the environment (VERSION, COMMIT, DATE, REPO, COVER_MIN, SITE_URL).

set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

module    := "github.com/yorch/ccshelf"
cmd       := "./cmd/ccshelf"
dist      := "dist"
version   := env("VERSION", "dev")
commit    := env("COMMIT", `git rev-parse --short=12 HEAD 2>/dev/null || echo none`)
date      := env("DATE", `git log -1 --format=%cs 2>/dev/null || date -u +%Y-%m-%d`)
repo      := env("REPO", "yorch/ccshelf")
ldflags   := "-s -w" + \
  " -X " + module + "/internal/version.Version=" + version + \
  " -X " + module + "/internal/version.Commit=" + commit + \
  " -X " + module + "/internal/version.Date=" + date + \
  " -X " + module + "/internal/version.Repo=" + repo
bin       := dist / "ccshelf" + `go env GOEXE`
cover_min := env("COVER_MIN", "70")
site_url  := env("SITE_URL", "https://site.example.test/ccshelf")

# Pinned versions of the development tools (keep in step with .github/workflows/ci.yml).
golangci_lint_version := "v2.14.0"
govulncheck_version   := "v1.8.0"
goreleaser_version    := "v2.18.2"
actionlint_version    := "v1.7.12"

targets := "darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64"

# List the recipes
[private]
default:
    @{{just_executable()}} --justfile {{justfile()}} --list --unsorted

# Build the ccshelf binary into dist/
build:
    CGO_ENABLED=0 go build -trimpath -ldflags "{{ldflags}}" -o {{bin}} {{cmd}}

# Run the unit tests (default ./...; pass packages or flags to narrow, e.g. `just test ./internal/config -run Edit`)
test *args="./...":
    go test -count=1 {{args}}

# Run the unit tests with the race detector (same arguments as test)
test-race *args="./...":
    go test -race -count=1 {{args}}

# Coverage: HTML report in dist/coverage.html and a threshold check (cover_min, default 70)
cover:
    @mkdir -p {{dist}}
    go test -count=1 -covermode=atomic -coverprofile={{dist}}/coverage.out ./...
    go tool cover -html={{dist}}/coverage.out -o {{dist}}/coverage.html
    scripts/check-cover.sh {{dist}}/coverage.out {{cover_min}}

# Run golangci-lint (install with `just tools`)
lint:
    golangci-lint run ./...

# Format the code (gofumpt through golangci-lint)
fmt:
    golangci-lint fmt ./...

# Fail if any file is not gofmt-clean
fmt-check:
    @out="$(gofmt -l .)"; if [ -n "$out" ]; then echo "needs gofmt:"; echo "$out"; exit 1; fi

# Run go vet
vet:
    go vet ./...

# Run govulncheck (install with `just tools`)
vuln:
    govulncheck ./...

# Cross-compile all six targets into dist/
cross:
    @mkdir -p {{dist}}
    @for t in {{targets}}; do \
      os=${t%/*}; arch=${t#*/}; ext=""; [ "$os" = windows ] && ext=".exe"; \
      echo "build $os/$arch"; \
      CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "{{ldflags}}" \
        -o "{{dist}}/ccshelf-$os-$arch$ext" {{cmd}}; \
    done

# Rebuild docs/report.html from the Markdown
docs:
    python3 docs/build_report.py

# Test the website validator and docs builder, then build and validate dist/site (Python 3 only)
site-check:
    python3 -I scripts/test_check_site.py
    python3 -I scripts/test_build_docs.py
    bash scripts/check-site.sh

# Build the deployable website (site/ plus the generated docs) into dist/site and validate it
site-build:
    bash scripts/build-site.sh {{dist}}/site {{site_url}}

# Render only the docs pages into dist/site/docs (no validation; run site-build for a full build)
docs-site:
    @mkdir -p {{dist}}/site
    python3 -I scripts/build_docs.py --out {{dist}}/site

# Regenerate docs/reference/cli.md from the real binary
cli-reference:
    bash scripts/gen-cli-reference.sh --write

# Fail if docs/reference/cli.md is out of date
cli-reference-check:
    bash scripts/gen-cli-reference.sh --check

# Fail if docs/report.html is out of date
docs-check:
    python3 docs/build_report.py --check

# Run lint, compile --check and catalog build on examples/org-data-repo
examples-check: build
    scripts/check-examples.sh {{bin}}

# Run the end-to-end tests of the built binary against a fake claude
e2e:
    go test -race -count=1 ./internal/e2e/...

# Run the offline tests of the Action installer
action-test:
    bash action/test/run.sh

# Fail if a workflow `uses:` is not pinned by full SHA
pins-check:
    scripts/check-pins.sh

# Check relative Markdown links
links-check:
    scripts/check-links.sh

# Check LF endings and final newlines
eol-check:
    scripts/check-eol.sh

# Build a local goreleaser snapshot (no signing, no publishing)
snapshot:
    goreleaser release --snapshot --clean --skip=sign,sbom

# Install the pinned development tools into $(go env GOPATH)/bin
tools:
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@{{golangci_lint_version}}
    go install golang.org/x/vuln/cmd/govulncheck@{{govulncheck_version}}
    go install github.com/goreleaser/goreleaser/v2@{{goreleaser_version}}
    go install github.com/rhysd/actionlint/cmd/actionlint@{{actionlint_version}}

# Remove build output and coverage files
clean:
    rm -rf {{dist}} coverage.out coverage.html

# What CI runs
ci: fmt-check vet lint test-race vuln pins-check action-test docs-check cli-reference-check links-check eol-check site-check examples-check
    @if command -v actionlint >/dev/null 2>&1; then actionlint; else echo "actionlint not installed; skipped (just tools)"; fi
