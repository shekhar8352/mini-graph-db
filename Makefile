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

.PHONY: all build test lint fmt proto proto-check bench cover

BUF         ?= buf
PROTOC_GEN_GO := protoc-gen-go
PROTOC_GEN_GO_GRPC := protoc-gen-go-grpc
BUF_VERSION ?= v1.57.2
PROTOC_GEN_GO_VERSION ?= v1.36.6
PROTOC_GEN_GO_GRPC_VERSION ?= v1.5.1

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
	@if ! command -v $(BUF) >/dev/null 2>&1; then \
		$(GO) install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION); \
	fi
	@if ! command -v $(PROTOC_GEN_GO) >/dev/null 2>&1; then \
		$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION); \
	fi
	@if ! command -v $(PROTOC_GEN_GO_GRPC) >/dev/null 2>&1; then \
		$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION); \
	fi
	PATH="$(GOPATH_BIN):$$PATH" $(BUF) generate

proto-check: proto
	git diff --exit-code -- api/gen api/proto buf.yaml buf.gen.yaml

bench:
	$(GO) test -run=^$$ -bench=. -benchmem ./...

cover:
	$(GO) test -race -coverprofile=cover.out ./...
	$(GO) tool cover -html=cover.out -o coverage.html
