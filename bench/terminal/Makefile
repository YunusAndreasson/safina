.PHONY: native compare ghostty-contracts bench

NATIVE_PREFIX ?= $(CURDIR)/.build/native
export PKG_CONFIG_PATH := $(NATIVE_PREFIX)/lib/pkgconfig

native:
	python3 build_native.py

# Baseline failures are intentional: these contracts express desired behavior.
# This comparison is separate from Safina's ordinary CI gate.
compare:
	go test -tags ghostty -v -timeout 60s ./...

ghostty-contracts:
	go test -tags ghostty -run '/ghostty' -timeout 60s ./...

bench:
	go test -tags ghostty -run '^$$' -bench . -benchmem -count 3 -cpu 1
