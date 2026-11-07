.PHONY: build install dev clean test test-race verify verify-all site-verify tidy lint lint-fix fmt bench-build bench-seed bench-teardown bench-quick bench-full bench-record

BINARY_NAME = safina
BUILD_DIR = bin
MAIN_PATH = cmd/safina/main.go
GOLANGCI_LINT_VERSION = v2.15.0
GOLANGCI_LINT = go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GO_BIN_DIR = $(shell go env GOBIN)
ifeq ($(strip $(GO_BIN_DIR)),)
GO_BIN_DIR = $(shell go env GOPATH | cut -d ':' -f 1)/bin
endif

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -X github.com/yunus/safina/internal/version.Version=$(VERSION)

BENCH_BINARY = $(BUILD_DIR)/safina-bench
BENCH_CONTEXT ?= safina-bench
BENCH_KUBECONFIG ?= $(HOME)/.kube/config

build:
	go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)

install: build
	install -d "$(GO_BIN_DIR)"
	install -m 755 "$(BUILD_DIR)/$(BINARY_NAME)" "$(GO_BIN_DIR)/$(BINARY_NAME)"

dev:
	go run $(MAIN_PATH)

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./...

test-race:
	go test -race ./...

# Same checks locally and in CI. No live cluster is needed.
verify:
	$(GOLANGCI_LINT) config verify
	go mod tidy -diff
	go mod verify
	$(GOLANGCI_LINT) fmt --diff ./cmd/... ./internal/...
	$(GOLANGCI_LINT) run ./...
	go test -race ./...
	go build ./...

site-verify:
	cd site && pnpm verify

verify-all: verify site-verify

tidy:
	go mod tidy

lint:
	$(GOLANGCI_LINT) run ./...

lint-fix:
	$(GOLANGCI_LINT) run --fix ./...

fmt:
	$(GOLANGCI_LINT) fmt ./cmd/... ./internal/...

# --- benchmark harness ------------------------------------------------------

bench-build:
	go build -o $(BENCH_BINARY) ./cmd/safina-bench

bench-seed:
	./scripts/bench/seed.sh

bench-teardown:
	./scripts/bench/teardown.sh

# Smoke run: V0 baseline + V11 combo on the two pod-list scenarios. Gates on
# bench/baseline.json — non-zero exit if p95 > 15% over recorded baseline.
bench-quick: bench-build
	$(BENCH_BINARY) --kubeconfig $(BENCH_KUBECONFIG) --context $(BENCH_CONTEXT) --quick

# Full grid: every variant × every scenario, N=30 with 5 warmup. Multi-minute
# run; writes CSV + Markdown into bench/out/.
bench-full: bench-build
	$(BENCH_BINARY) --kubeconfig $(BENCH_KUBECONFIG) --context $(BENCH_CONTEXT) \
	    --variants=all --scenarios=all --n=30 --warmup=5

# Run the full grid and OVERWRITE bench/baseline.json with this run's p95
# numbers. Reserve for "we trust this run, lock it in" moments after a
# topology change or a deliberate accepted regression.
bench-record: bench-build
	$(BENCH_BINARY) --kubeconfig $(BENCH_KUBECONFIG) --context $(BENCH_CONTEXT) \
	    --variants=all --scenarios=all --n=30 --warmup=5 --record-baseline
