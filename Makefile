.PHONY: build test check release-check release-snapshot enroll-key signing-check release-tag sign-release

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)

build:
	go build -ldflags "-X github.com/sliplane/cli/internal/buildinfo.Version=$(VERSION) -X github.com/sliplane/cli/internal/buildinfo.Commit=$(COMMIT)" -o bin/sliplane ./cmd/sliplane

test:
	go test ./...

check:
	go vet ./...
	go test -race ./...

release-check:
	goreleaser check

release-snapshot: release-check
	goreleaser release --snapshot --clean

# Release tasks that need a maintainer YubiKey; see docs/releasing.md.
KEY ?= yubikey-main

enroll-key:
	scripts/release.sh enroll "$(KEY)"

signing-check:
	scripts/release.sh check "$(KEY)"

release-tag:
	scripts/release.sh tag "$(TAG)" "$(KEY)"

sign-release:
	scripts/release.sh sign "$(TAG)" "$(KEY)"
