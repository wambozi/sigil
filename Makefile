SHELL := bash
GO    := go

.PHONY: fmt vet test check build

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

## check runs fmt, vet, and test in sequence — use this before committing.
check: fmt vet test

build:
	$(GO) build ./cmd/sigild/
	$(GO) build ./cmd/sigilctl/
