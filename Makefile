MODULE      := github.com/shekhar8352/mini-graph-db
VERSION     ?= 0.0.0-dev
COMMIT      := $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
DATE        := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -X '$(MODULE)/internal/version.Version=$(VERSION)' \
               -X '$(MODULE)/internal/version.Commit=$(COMMIT)' \
               -X '$(MODULE)/internal/version.BuildDate=$(DATE)'

GO          ?= go
BIN         := bin/graphdb
GOLANGCI    ?= golangci-lint
GOPATH_BIN  := $(shell $(GO) env GOPATH)/bin

.PHONY: all build test lint fmt proto bench cover

all: build test

build:
	mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/graphdb

test:
	$(GO) test -race -count=1 ./...

lint:
	@if command -v $(GOLANGCI) >/dev/null 2>&1; then \
		LINT=$(GOLANGCI); \
	elif [ -x "$(GOPATH_BIN)/golangci-lint" ]; then \
		LINT=$(GOPATH_BIN)/golangci-lint; \
	else \
		echo "golangci-lint is required: https://golangci-lint.run/welcome/install/"; \
		exit 1; \
	fi; \
	$$LINT run ./...

fmt:
	$(GO) fmt ./...
	@command -v goimports >/dev/null 2>&1 && goimports -w -local $(MODULE) . || \
		$(GO) run golang.org/x/tools/cmd/goimports@v0.37.0 -w -local $(MODULE) .

proto:
	@echo "proto generation is not implemented yet (Phase 6)"

bench:
	$(GO) test -run=^$$ -bench=. -benchmem ./...

cover:
	$(GO) test -race -coverprofile=cover.out ./...
	$(GO) tool cover -html=cover.out -o coverage.html
