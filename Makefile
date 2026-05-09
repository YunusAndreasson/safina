.PHONY: build install dev clean test

BINARY_NAME = safina
BUILD_DIR = bin
MAIN_PATH = cmd/safina/main.go

build:
	go build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)

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
