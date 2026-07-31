VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
BIN     := bin/sbctl

.PHONY: all build test vet fmt lint check install uninstall dev clean cross

all: check build

## build: compile the sbctl binary
build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/sbctl

## test: run the test suite
test:
	go test ./...

## vet: run go vet
vet:
	go vet ./...

## fmt: rewrite sources with gofmt
fmt:
	gofmt -w .

## lint: run golangci-lint when it is available
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint is not installed; skipping (see .golangci.yml)"; \
	fi

## check: everything that must pass before a change lands
check: fmt-check vet test

# fmt-check fails rather than rewriting, so CI cannot silently pass unformatted code.
.PHONY: fmt-check
fmt-check:
	@unformatted="$$(gofmt -l . )"; \
	if [ -n "$$unformatted" ]; then \
		echo "these files need gofmt:"; echo "$$unformatted"; exit 1; \
	fi

## cross: verify the tree still compiles for every supported platform
cross:
	GOOS=darwin  go build ./... 
	GOOS=linux   go build ./...
	GOOS=windows go build ./...

## install: build and install sbctl plus its system integration
install: build
	sudo install -m 755 $(BIN) /usr/local/bin/sbctl
	sudo bash scripts/install.sh

## uninstall: remove sbctl and its system integration
uninstall:
	sudo bash scripts/uninstall.sh

## dev: run from source
dev:
	go run ./cmd/sbctl

clean:
	rm -rf bin
