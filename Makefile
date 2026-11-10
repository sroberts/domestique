# Domestique build. The Go binary embeds the PWA, so build web first.
# CI runs these same targets (.github/workflows/ci.yml), so a green
# `make check` locally means a green CI run.
PORT    ?= 8099
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
STORAGE ?= $(CURDIR)/.data

# golangci-lint at the version CI pins; without a local install, go run
# fetches exactly that version rather than whatever is newest.
GOLANGCI_VERSION := v2.15.0
GOLANGCI ?= $(shell command -v golangci-lint 2>/dev/null || echo "go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)")

# Total statement coverage CI requires, in percent. A ratchet: raise it as
# coverage grows, never lower it to make a change pass.
COVER_MIN := 46

.PHONY: help all web web-check build test test-race cover vet lint fmt fmt-check tidy conformance check eval run dev release clean

help: ## List targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

all: build

web: ## Install, type-check, test and build the PWA into internal/webui/dist
	cd web && npm ci && npm run check && npm test && npm run build

web-check: ## Type-check and test the PWA (needs web/node_modules)
	cd web && npm run check && npm test

build: web ## Build the binary into ./bin
	go build -trimpath -o bin/domestique .

test: ## Run the Go test suite
	go test ./...

# The race detector needs cgo; everything else stays cgo-free so the release
# binary is static (modernc.org/sqlite is pure Go).
test-race: ## Run the Go test suite under the race detector
	CGO_ENABLED=1 go test -race ./...

cover: ## Test with coverage and fail below COVER_MIN percent
	go test -count=1 -coverprofile=coverage.out -covermode=atomic ./...
	@total=$$(go tool cover -func=coverage.out | awk '/^total:/ {gsub("%","",$$3); print $$3}'); \
	echo "coverage: $$total% (minimum $(COVER_MIN)%)"; \
	awk -v c="$$total" -v m="$(COVER_MIN)" 'BEGIN { exit (c+0 < m+0) }' || { echo "coverage $$total% is below $(COVER_MIN)%"; exit 1; }

vet: ## go vet
	go vet ./...

lint: vet ## go vet plus golangci-lint
	$(GOLANGCI) run ./...

fmt: ## Format all Go files
	gofmt -l -w $$(git ls-files '*.go')

fmt-check: ## Fail if any Go file is unformatted
	@out=$$(gofmt -l $$(git ls-files '*.go')); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi; \
	echo "gofmt: clean"

tidy: ## Fail if go.mod/go.sum are not tidy
	go mod tidy -diff

conformance: ## Run the built binary against the Outpost service checklist
	bash scripts/conformance.sh

check: fmt-check tidy lint cover test-race web-check conformance ## Everything CI runs

# Score the models on Domestique's questions (internal/coach/eval). With
# SYSTEM1 or LLM set, against Ollama on this machine; otherwise through
# Outpost, from OUTPOST_AI_URL and OUTPOST_AI_TOKEN.
#   make eval SYSTEM1=laya
OLLAMA ?= http://127.0.0.1:11434
eval: ## Score the models on fixed cases (SYSTEM1=laya for local Ollama)
	go run ./cmd/eval $(if $(SYSTEM1)$(LLM),-ollama $(OLLAMA) -system1 '$(SYSTEM1)' -llm '$(LLM)') $(EVAL_ARGS)

# Run locally with demo Whoop data (recovery, heart rate, strain).
run: build ## Build, then run with demo data on PORT
	mkdir -p $(STORAGE)
	OUTPOST_STORAGE_DIR=$(STORAGE) DOMESTIQUE_DEMO=1 ./bin/domestique --port $(PORT)

# Vite dev server on :5173 proxies /v1 to the Go service on $(PORT).
dev: ## Vite dev server, proxying /v1 to PORT
	cd web && npm run dev

# Release asset for the Mac Mini, named so `outpost add` can find it.
release: web ## Build the darwin/arm64 release asset into ./dist
	mkdir -p dist
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o dist/domestique-darwin-arm64 .
	cd dist && shasum -a 256 domestique-* > SHA256SUMS 2>/dev/null || sha256sum domestique-* > SHA256SUMS
	cp outpost.toml dist/

clean: ## Remove build output
	rm -rf bin dist coverage.out
