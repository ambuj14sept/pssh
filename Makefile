.PHONY: all build clean install test

VERSION := 1.0.0
BINARY := pssh
DAEMON := psshd
INSTALL_DIR := /usr/local/bin

GO := go
GOFLAGS := -ldflags="-s -w -X main.version=$(VERSION)"

all: build

build: build-daemons build-client

build-daemons:
	@echo "Building daemon (linux/amd64)..."
	@mkdir -p bin
	@GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(GOFLAGS) -o bin/$(DAEMON)_linux_amd64 ./cmd/$(DAEMON)
	@echo "Building daemon (linux/arm64)..."
	@GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(GOFLAGS) -o bin/$(DAEMON)_linux_arm64 ./cmd/$(DAEMON)

build-client: build-daemons
	@echo "Building client..."
	@cp bin/$(DAEMON)_linux_amd64 pkg/client/psshd_linux_amd64
	@cp bin/$(DAEMON)_linux_arm64 pkg/client/psshd_linux_arm64
	@$(GO) build $(GOFLAGS) -o bin/$(BINARY) ./cmd/$(BINARY)

clean:
	@echo "Cleaning..."
	@rm -rf bin/
	@rm -f pkg/client/psshd_linux_amd64
	@rm -f pkg/client/psshd_linux_arm64

install: build
	@echo "Installing to $(INSTALL_DIR)..."
	@cp bin/$(BINARY) $(INSTALL_DIR)/$(BINARY)
	@chmod +x $(INSTALL_DIR)/$(BINARY)

test:
	@$(GO) test -v ./...

fmt:
	@$(GO) fmt ./...

deps:
	@$(GO) mod tidy
	@$(GO) mod download
