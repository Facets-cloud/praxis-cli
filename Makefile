BINARY  := praxis
PKG     := github.com/Facets-cloud/praxis-cli
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
  -X $(PKG)/cmd.version=$(VERSION) \
  -X $(PKG)/cmd.commit=$(COMMIT) \
  -X $(PKG)/cmd.date=$(DATE)

.PHONY: build install test clean fmt vet lint

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

install:
	go install -ldflags "$(LDFLAGS)" .

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

lint: fmt vet test

clean:
	rm -f $(BINARY)
