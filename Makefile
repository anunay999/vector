BINARY := vector
PKG := github.com/anunay999/vector
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(PKG)/internal/version.Version=$(VERSION) \
           -X $(PKG)/internal/version.Commit=$(COMMIT) \
           -X $(PKG)/internal/version.Date=$(DATE)

.PHONY: build test race vet fmt lint tidy install run clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/vector

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w internal cmd

lint: vet
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed; skipping"

tidy:
	go mod tidy

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/vector

run: build
	./bin/$(BINARY) serve

clean:
	rm -rf bin
