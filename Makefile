.PHONY: all build assets lint test check sdk-lint sdk-test run fmt

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

sdk-lint:
	cd sdk/configcenter && TMPDIR=$(TMP_ROOT) golangci-lint run ./...

sdk-test:
	cd sdk/configcenter && $(GO_TEST) ./...

check: assets lint sdk-lint test sdk-test

# The binary loads ConfigCenter connection bootstrap from .env.local;
# operational settings are read from ConfigCenter through the SDK.
run: build
	exec ./$(BIN)
