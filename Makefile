APP_NAME := hydra-node
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)"

.PHONY: build build-all clean test docker

# Build for current platform
build:
	CGO_ENABLED=1 go build $(LDFLAGS) -o bin/$(APP_NAME) ./cmd/hydra-node

# Cross-compile for all supported platforms
build-all: build-linux-amd64 build-linux-arm64 build-darwin-amd64 build-darwin-arm64 build-windows-amd64

build-linux-amd64:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build $(LDFLAGS) -o bin/$(APP_NAME)-linux-amd64 ./cmd/hydra-node

build-linux-arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc go build $(LDFLAGS) -o bin/$(APP_NAME)-linux-arm64 ./cmd/hydra-node

build-darwin-amd64:
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build $(LDFLAGS) -o bin/$(APP_NAME)-darwin-amd64 ./cmd/hydra-node

build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build $(LDFLAGS) -o bin/$(APP_NAME)-darwin-arm64 ./cmd/hydra-node

build-windows-amd64:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 go build $(LDFLAGS) -o bin/$(APP_NAME)-windows-amd64.exe ./cmd/hydra-node

# Run tests
test:
	go test ./... -v -race

# Build Docker image
docker:
	docker build -t hydra-node:$(VERSION) -t hydra-node:latest .

# Clean build artifacts
clean:
	rm -rf bin/

# Run locally
run: build
	./bin/$(APP_NAME) start --config configs/node.example.yaml

# Download Go dependencies
deps:
	go mod download
	go mod tidy

# Format code
fmt:
	gofmt -s -w .

# Lint
lint:
	golangci-lint run ./...
