SHELL := bash
GO    := go

COV_MIN := 50

.PHONY: fmt vet test check build install run status generate coverage sync-assets

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

build: sync-assets
	$(GO) build ./cmd/sigild/
	$(GO) build ./cmd/sigilctl/
	$(GO) build ./plugins/sigil-plugin-claude/
	$(GO) build ./plugins/sigil-plugin-github/

## sync-assets copies shell hooks and service files into the embed directory
## so go:embed can bundle them into the binary.
sync-assets:
	@cp scripts/shell-hook.zsh  internal/assets/scripts/shell-hook.zsh
	@cp scripts/shell-hook.bash internal/assets/scripts/shell-hook.bash
	@cp deploy/sigild.service   internal/assets/deploy/sigild.service

## install builds and installs sigild + sigilctl to $GOPATH/bin, then runs init.
install: build
	$(GO) install ./cmd/sigild/ ./cmd/sigilctl/ ./plugins/sigil-plugin-claude/ ./plugins/sigil-plugin-github/
	@echo ""
	@echo "Installed to $$($(GO) env GOPATH)/bin/"
	@echo "Run 'sigild init' to complete setup."

## sync-assets copies shell hooks and service files into the embed directory
## so go:embed can bundle them into the binary.
sync-assets:
	@cp scripts/shell-hook.zsh  internal/assets/scripts/shell-hook.zsh
	@cp scripts/shell-hook.bash internal/assets/scripts/shell-hook.bash
	@cp deploy/sigild.service   internal/assets/deploy/sigild.service

## install builds and installs sigild + sigilctl to $GOPATH/bin, then runs init.
install: build
	$(GO) install ./cmd/sigild/ ./cmd/sigilctl/
	@echo ""
	@echo "Installed to $$($(GO) env GOPATH)/bin/"
	@echo "Run 'sigild init' to complete setup."

## run builds and starts sigild with the dev config, watching ~/workspace.
## Set SIGIL_CLOUD_API_KEY in your environment for cloud inference.
run: build
	@mkdir -p ~/.local/share/sigild
	./sigild -config dev.toml

## coverage runs tests with coverage and fails if internal/ drops below COV_MIN%.
coverage:
	@$(GO) test ./internal/... -coverprofile=cover.out -covermode=atomic > /dev/null 2>&1
	@total=$$($(GO) tool cover -func=cover.out | awk '/^total:/ {gsub(/%/,"",$$NF); print $$NF}'); \
	echo "Internal coverage: $${total}% (minimum: $(COV_MIN)%)"; \
	if [ $$(echo "$${total} < $(COV_MIN)" | bc -l) -eq 1 ]; then \
		echo "FAIL: coverage $${total}% is below $(COV_MIN)% gate"; \
		exit 1; \
	fi
	@rm -f cover.out

## status queries the running daemon via sigilctl.
status: build
	./sigilctl status
