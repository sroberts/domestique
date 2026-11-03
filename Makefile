# Domestique build. The Go binary embeds the PWA, so build web first.
PORT    ?= 8099
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
STORAGE ?= $(CURDIR)/.data

.PHONY: all web build test run dev release clean

all: build

web:
	cd web && npm ci && npm run check && npm run build

build: web
	go build -trimpath -o bin/domestique .

test:
	go vet ./... && go test ./...

# Run locally with demo Whoop data (recovery, heart rate, strain).
run: build
	mkdir -p $(STORAGE)
	OUTPOST_STORAGE_DIR=$(STORAGE) DOMESTIQUE_DEMO=1 ./bin/domestique --port $(PORT)

# Vite dev server on :5173 proxies /v1 to the Go service on $(PORT).
dev:
	cd web && npm run dev

# Release asset for the Mac Mini, named so `outpost add` can find it.
release: web
	mkdir -p dist
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o dist/domestique-darwin-arm64 .
	cd dist && shasum -a 256 domestique-* > SHA256SUMS 2>/dev/null || sha256sum domestique-* > SHA256SUMS
	cp outpost.toml dist/

clean:
	rm -rf bin dist
