VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build install uninstall dev fmt vet

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/sbctl .

install: build
	sudo install -m 755 bin/sbctl /usr/local/bin/sbctl
	sudo bash scripts/install.sh

uninstall:
	sudo bash scripts/uninstall.sh
	sudo rm -f /usr/local/bin/sbctl

dev:
	go run .

fmt:
	gofmt -w .

vet:
	go vet ./...
