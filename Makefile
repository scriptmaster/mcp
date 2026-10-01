GO ?= go
DIST := dist
PACKAGE := ./cmd/server
BUILD_FLAGS := -buildvcs=false -trimpath

.PHONY: downloads windows-amd64 windows-arm64 linux-amd64 linux-arm64 macos-amd64 macos-arm64 test

downloads: windows-amd64 windows-arm64 linux-amd64 linux-arm64 macos-amd64 macos-arm64
	cd $(DIST) && sha256sum mcp-server-* > SHA256SUMS

windows-amd64:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build $(BUILD_FLAGS) -o $(DIST)/mcp-server-windows-amd64.exe $(PACKAGE)

windows-arm64:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GO) build $(BUILD_FLAGS) -o $(DIST)/mcp-server-windows-arm64.exe $(PACKAGE)

linux-amd64:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(BUILD_FLAGS) -o $(DIST)/mcp-server-linux-amd64 $(PACKAGE)

linux-arm64:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(BUILD_FLAGS) -o $(DIST)/mcp-server-linux-arm64 $(PACKAGE)

macos-amd64:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build $(BUILD_FLAGS) -o $(DIST)/mcp-server-macos-amd64 $(PACKAGE)

macos-arm64:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build $(BUILD_FLAGS) -o $(DIST)/mcp-server-macos-arm64 $(PACKAGE)

test:
	$(GO) test ./...
