BINARY  := praxis
PKG     := github.com/Facets-cloud/praxis-cli
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
  -X $(PKG)/cmd.version=$(VERSION) \
  -X $(PKG)/cmd.commit=$(COMMIT) \
  -X $(PKG)/cmd.date=$(DATE)

GOLANGCI_LINT_VERSION ?= v2.13.2

.PHONY: build install test clean fmt vet lint check

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

install:
	go install -ldflags "$(LDFLAGS)" .

test:
	go test -race ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

check: fmt vet lint test

clean:
	rm -f $(BINARY)
