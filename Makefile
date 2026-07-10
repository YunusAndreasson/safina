.PHONY: build install dev clean test lint lint-fix fmt bench-build bench-seed bench-teardown bench-quick bench-full bench-record

BINARY_NAME = safina
BUILD_DIR = bin
MAIN_PATH = cmd/safina/main.go

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -X github.com/yunus/safina/internal/version.Version=$(VERSION)

BENCH_BINARY = $(BUILD_DIR)/safina-bench
BENCH_CONTEXT ?= safina-bench
BENCH_KUBECONFIG ?= $(HOME)/.kube/config

build:
	go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)

install: build
	cp $(BUILD_DIR)/$(BINARY_NAME) $(GOPATH)/bin/

dev:
	go run $(MAIN_PATH)

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./...

tidy:
	go mod tidy

lint:
	golangci-lint run ./...

lint-fix:
	golangci-lint run --fix ./...

fmt:
	golangci-lint fmt ./...

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
