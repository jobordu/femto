# femto — build / test / cross-compile / images
BINARY   := femto
PKG      := ./cmd/femto
IMAGE    := femto
LDFLAGS  := -s -w
GOFLAGS  := -trimpath -ldflags='$(LDFLAGS)'

.PHONY: all build test cover vet fmt clean cross image image-cross sandboxes

all: test build

build: ## static binary for the host arch
	CGO_ENABLED=0 go build $(GOFLAGS) -o bin/$(BINARY) $(PKG)
	@echo "built bin/$(BINARY) ($$(du -h bin/$(BINARY) | cut -f1))"

test: ## run all tests
	go test ./...

cover: ## coverage summary
	go test -cover ./...

vet: ; go vet ./...
fmt: ; gofmt -w .

cross: ## static binaries for linux/{amd64,arm64} (Go cross-compiles, no QEMU)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -o bin/$(BINARY)-linux-amd64 $(PKG)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -o bin/$(BINARY)-linux-arm64 $(PKG)
	@ls -lh bin/$(BINARY)-linux-*

image: ## build the scratch agent image (host arch)
	docker build -t $(IMAGE):latest .
	@docker images $(IMAGE):latest --format '  {{.Repository}}:{{.Tag}} {{.Size}}'

image-cross: ## multi-arch agent image via buildx
	docker buildx build --platform linux/arm64,linux/amd64 -t $(IMAGE):latest .

sandboxes: ## build the specialized sandbox tiers (lite/crypto/pwn/full)
	./infra/sandbox/build.sh

clean: ; rm -rf bin

help: ## list targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  %-14s %s\n", $$1, $$2}'
