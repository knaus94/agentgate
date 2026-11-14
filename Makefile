VERSION ?= 0.15.0
GO ?= go
LDFLAGS = -s -w -X github.com/knaus94/agentgate/internal/update.Version=$(VERSION)

.PHONY: build fmt-check vet test design-unit design-browser check check-all binaries package clean

build:
	mkdir -p dist
	$(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o dist/agentgate ./cmd/agentgate

fmt-check:
	@test -z "$$(gofmt -l cmd internal design-kit/*.go)" || { gofmt -l cmd internal design-kit/*.go; exit 1; }

vet:
	$(GO) vet ./...

test:
	$(GO) test -race ./...

design-unit:
	node --test --test-concurrency=1 design-kit/tests/core.test.mjs

design-browser:
	node --test --test-concurrency=1 design-kit/tests/workflow.test.mjs

check: fmt-check vet test design-unit

check-all: check design-browser

binaries:
	@for arch in amd64 arm64; do \
		mkdir -p dist/linux-$$arch; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch $(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o dist/linux-$$arch/agentgate ./cmd/agentgate || exit 1; \
	done

package: binaries
	python3 scripts/package.py --version '$(VERSION)'

clean:
	rm -rf dist
