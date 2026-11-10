.PHONY: build install dev clean test test-race verify verify-all site-verify tidy lint lint-fix fmt bench-build bench-seed bench-teardown bench-quick bench-full bench-record versus-build versus-k9s versus-quick versus

BINARY_NAME = safina
.DEFAULT_GOAL := build
BUILD_DIR = bin
MAIN_PATH = ./cmd/safina
NATIVE_CACHE ?= $(CURDIR)/.build/libghostty
export PKG_CONFIG_PATH := $(NATIVE_CACHE)/native/lib/pkgconfig:$(PKG_CONFIG_PATH)
GOLANGCI_LINT_VERSION = v2.15.0
GOLANGCI_LINT = go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GO_BIN_DIR = $(shell go env GOBIN)
ifeq ($(strip $(GO_BIN_DIR)),)
GO_BIN_DIR = $(shell go env GOPATH | cut -d ':' -f 1)/bin
endif

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -X github.com/YunusAndreasson/safina/internal/version.Version=$(VERSION)

BENCH_BINARY = $(BUILD_DIR)/safina-bench
BENCH_CONTEXT ?= safina-bench
BENCH_KUBECONFIG ?= $(HOME)/.kube/config

.PHONY: native
native:
	python3 scripts/build-libghostty.py --cache "$(NATIVE_CACHE)"

build dev test test-race verify lint lint-fix fmt bench-build versus-build: native

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

# --- safina vs k9s ----------------------------------------------------------
# Both tools run in a pseudo-terminal against the bench cluster through one
# recording proxy. safina is built like a release; k9s is the pinned release
# from bench/versus/versions.json. Reports land in bench/versus/out/.

VERSUS_BINARY = $(BUILD_DIR)/safina-versus
VERSUS_SAFINA = $(BUILD_DIR)/safina-release
VERSUS_ARGS = --kubeconfig $(BENCH_KUBECONFIG) --context $(BENCH_CONTEXT) --safina $(VERSUS_SAFINA) --k9s bench/.build/k9s

versus-build:
	go build -o $(VERSUS_BINARY) ./cmd/safina-versus
	go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o $(VERSUS_SAFINA) $(MAIN_PATH)

versus-k9s:
	./scripts/bench/k9s.sh

# A few minutes: three rounds, every operation, 10s idle per launch.
versus-quick: versus-build versus-k9s
	$(VERSUS_BINARY) run $(VERSUS_ARGS) --rounds 3 --idle 10s

# The full comparison: ten rounds on the local cluster, then again with
# 40 ms added to every API request, the way a remote cluster answers.
versus: versus-build versus-k9s
	$(VERSUS_BINARY) run $(VERSUS_ARGS) --rounds 10 --idle 30s
	$(VERSUS_BINARY) run $(VERSUS_ARGS) --rounds 10 --idle 30s --rtt 40ms
