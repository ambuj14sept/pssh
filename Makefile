.PHONY: all build clean install test

VERSION := 1.0.0
BINARY := pssh
DAEMON := psshd
INSTALL_DIR := /usr/local/bin

GO := go
GOFLAGS := -ldflags="-s -w -X main.version=$(VERSION)"

all: build

build: build-daemon build-client

build-daemon:
	@echo "Building daemon..."
	@mkdir -p bin
	@$(GO) build $(GOFLAGS) -o bin/$(DAEMON) ./cmd/$(DAEMON)

build-client: build-daemon
	@echo "Building client..."
	@cp bin/$(DAEMON) pkg/client/psshd
	@$(GO) build $(GOFLAGS) -o bin/$(BINARY) ./cmd/$(BINARY)

clean:
	@echo "Cleaning..."
	@rm -rf bin/
	@rm -f pkg/client/psshd

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
