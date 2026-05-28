PROJECT_NAME := wsfold
GORELEASER_VERSION ?= 2.14.3
GO ?= go
GO_BUILD_FLAGS ?=
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GO_LDFLAGS := -X github.com/openclaw/wsfold/internal/buildinfo.Version=$(VERSION) -X github.com/openclaw/wsfold/internal/buildinfo.Commit=$(COMMIT) -X github.com/openclaw/wsfold/internal/buildinfo.Date=$(DATE)
GO_BUILD := CGO_ENABLED=0 $(GO) build $(GO_BUILD_FLAGS) -trimpath -ldflags "$(GO_LDFLAGS)"
GORELEASER := GORELEASER_VERSION=$(GORELEASER_VERSION) ./scripts/run-goreleaser.sh

.PHONY: test build native-bind-e2e release-check release-snapshot

test:
	$(GO) test ./...
	$(MAKE) native-bind-e2e

native-bind-e2e:
	mkdir -p dist
	$(GO_BUILD) -o dist/$(PROJECT_NAME)-native-bind-e2e ./cmd/wsfold
	WSFOLD_E2E_BINARY=dist/$(PROJECT_NAME)-native-bind-e2e bash ./scripts/native-bind-e2e.sh

build:
	mkdir -p dist
	$(GO_BUILD) -o dist/$(PROJECT_NAME) ./cmd/wsfold

release-check:
	$(GORELEASER) check

release-snapshot:
	$(GORELEASER) release --snapshot --clean
