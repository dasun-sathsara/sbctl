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

# System tray app targets
.PHONY: tray tray-windows tray-darwin

tray:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/sbctl-tray ./cmd/sbctl-tray/

tray-windows:
	GOOS=windows GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION) -H windowsgui" -o bin/sbctl-tray.exe ./cmd/sbctl-tray/

tray-darwin:
	GOOS=darwin GOARCH=arm64 go build -ldflags "-X main.version=$(VERSION)" -o bin/sbctl-tray-arm64 ./cmd/sbctl-tray/
	GOOS=darwin GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION)" -o bin/sbctl-tray-amd64 ./cmd/sbctl-tray/
