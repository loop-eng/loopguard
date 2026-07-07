BINARY := loopguard
MODULE := github.com/loop-eng/loopguard
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X '$(MODULE)/internal/cli.version=$(VERSION)' \
	-X '$(MODULE)/internal/cli.commit=$(COMMIT)' \
	-X '$(MODULE)/internal/cli.date=$(DATE)'

.PHONY: build test lint run clean install

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/loopguard

run: build
	./bin/$(BINARY)

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

install: build
	cp bin/$(BINARY) $(GOPATH)/bin/$(BINARY) 2>/dev/null || cp bin/$(BINARY) ~/go/bin/$(BINARY)

.DEFAULT_GOAL := build
