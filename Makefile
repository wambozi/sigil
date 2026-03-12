SHELL := bash
GO    := go

.PHONY: fmt vet test check build run status generate

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

## generate runs mockery to regenerate mock implementations.
generate:
	mockery

## check runs fmt, vet, and test in sequence — use this before committing.
check: fmt vet test

build:
	$(GO) build ./cmd/sigild/
	$(GO) build ./cmd/sigilctl/

## run builds and starts sigild with the dev config, watching ~/workspace.
## Set SIGIL_CLOUD_API_KEY in your environment for cloud inference.
run: build
	@mkdir -p ~/.local/share/sigild
	./sigild -config dev.toml

## status queries the running daemon via sigilctl.
status: build
	./sigilctl status
