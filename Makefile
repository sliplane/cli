.PHONY: build test check

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)

build:
	go build -ldflags "-X sliplane-cli/internal/buildinfo.Version=$(VERSION) -X sliplane-cli/internal/cli.commit=$(COMMIT)" -o bin/sliplane ./cmd/sliplane

test:
	go test ./...

check:
	go vet ./...
	go test -race ./...
