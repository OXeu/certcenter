.PHONY: all build assets lint test check run fmt

BIN ?= .tmp/bin/certcenter
TMP_ROOT := $(CURDIR)/.tmp
GO_TEST := mkdir -p $(TMP_ROOT) && TMPDIR=$(TMP_ROOT) go test

all: check build

build: assets
	mkdir -p $(TMP_ROOT)/bin
	TMPDIR=$(TMP_ROOT) go build -o $(BIN) ./cmd/certcenter

assets:
	npm run --silent build

fmt: assets
	golangci-lint fmt ./...
	npm run fmt

lint: assets
	TMPDIR=$(TMP_ROOT) golangci-lint run ./...
	npm run lint
	npm run typecheck
	npm test --silent

test: assets
	$(GO_TEST) ./...

check: assets lint test

# The binary loads ConfigCenter connection bootstrap from .env.local;
# operational settings are read from ConfigCenter through the internal configuration client.
run: build
	exec ./$(BIN)
