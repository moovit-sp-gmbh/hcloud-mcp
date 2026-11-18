BINARY    := hcloud-mcp
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
VERSION   := $(if $(VERSION),$(VERSION),0.0.0-dev)
# Local build output. dist/ belongs to goreleaser.
OUT       := build
CATALOG   := internal/catalog/generated/catalog.json
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64
LDFLAGS   := -s -w -X main.version=$(VERSION)

export CGO_ENABLED := 0

.PHONY: all generate build mcpb lint test snapshot clean check-catalog

## all: regenerate the catalog, cross-compile, package .mcpb bundles.
## Runs the steps strictly in order, even with make -j.
all:
	$(MAKE) generate
	$(MAKE) build
	$(MAKE) mcpb

## generate: load the OpenAPI specs from services.yaml and regenerate the embedded tool catalog
generate:
	go run ./cmd/gen -config services.yaml -out $(CATALOG)

check-catalog:
	@test -f $(CATALOG) || { echo "missing $(CATALOG): run 'make generate' first (needs HCLOUD_API_SERVER)"; exit 1; }

## build: cross-compile cgo-free binaries for all platforms into build/
build: check-catalog
	@mkdir -p $(OUT)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		out=$(OUT)/$(BINARY)_$${os}_$${arch}$$ext; \
		echo "building $$out"; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$out . || exit 1; \
	done

## mcpb: package the build/ binaries into .mcpb bundles (per platform + universal)
mcpb:
	go run ./cmd/mcpb -version $(VERSION) -dist $(OUT)

lint:
	golangci-lint run --timeout=5m ./...

test:
	go vet ./...
	go test ./...

## test+coverage: tests and coverage profile (coverage.txt), used by CI
test+coverage:
	go vet ./...
	go test -covermode=atomic -coverprofile=coverage.txt ./...

## snapshot: full goreleaser run (binaries, archives, .mcpb, docker images) without publishing
snapshot:
	goreleaser release --snapshot --clean

clean:
	go clean
	rm -f $(OUT)/$(BINARY)_*
