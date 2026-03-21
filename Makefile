SHELL  := bash
GO     := go
BIN    := ./bin

COV_MIN := 50

CMDS    := ./cmd/sigild/ ./cmd/sigilctl/
PLUGINS := $(wildcard ./plugins/sigil-plugin-*/)

.PHONY: all fmt fmt-check vet lint staticcheck test test-race check build install run \
        status generate coverage clean sync-assets hooks help

## all: default target — build everything.
all: build

## ---------- Formatting & Linting ------------------------------------------

## fmt: format all Go source files in place (all modules).
fmt:
	@gofmt -w .

## fmt-check: verify formatting without modifying files (CI-safe).
fmt-check:
	@test -z "$$(gofmt -l .)" || { echo "gofmt: the following files need formatting:"; gofmt -l .; exit 1; }

## vet: run go vet with shadow analysis.
vet:
	@$(GO) vet ./...

## lint: run staticcheck if installed, skip gracefully otherwise.
lint:
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; else echo "staticcheck not installed — skipping (go install honnef.co/go/tools/cmd/staticcheck@latest)"; fi

## ---------- Testing -------------------------------------------------------

## test: run all tests.
test:
	@$(GO) test ./...

## test-race: run all tests with the race detector enabled.
test-race:
	@$(GO) test -race ./...

## coverage: run tests with coverage and enforce COV_MIN% gate on internal/.
coverage:
	@$(GO) test ./internal/... -coverprofile=cover.out -covermode=atomic
	@total=$$($(GO) tool cover -func=cover.out | awk '/^total:/ {gsub(/%/,"",$$NF); print $$NF}'); \
	echo "Internal coverage: $${total}% (minimum: $(COV_MIN)%)"; \
	if [ $$(echo "$${total} < $(COV_MIN)" | bc -l) -eq 1 ]; then \
		echo "FAIL: coverage $${total}% is below $(COV_MIN)% gate"; \
		rm -f cover.out; \
		exit 1; \
	fi
	@rm -f cover.out

## ---------- Build ---------------------------------------------------------

## build: compile sigild, sigilctl, and all plugins into ./bin/.
build: sync-assets | $(BIN)
	@$(GO) build -o $(BIN)/ $(CMDS)
	@for p in $(PLUGINS); do $(GO) build -o $(BIN)/ $$p; done

$(BIN):
	@mkdir -p $(BIN)

## sync-assets: copy shell hooks and service files into the embed directory.
sync-assets:
	@cp scripts/shell-hook.zsh  internal/assets/scripts/shell-hook.zsh
	@cp scripts/shell-hook.bash internal/assets/scripts/shell-hook.bash
	@cp deploy/sigild.service   internal/assets/deploy/sigild.service

## install: build and install all binaries to $GOPATH/bin.
install: sync-assets
	@$(GO) install $(CMDS) $(PLUGINS)
	@echo ""
	@echo "Installed to $$($(GO) env GOPATH)/bin/"
	@echo "Run 'sigild init' to complete setup."

## ---------- CI gate -------------------------------------------------------

## check: CI-safe gate — verify formatting, vet, lint, test with race detector.
check: fmt-check vet lint test-race

## ---------- Dev helpers ---------------------------------------------------

## hooks: install git pre-commit hook (auto-formats Go on commit).
hooks:
	@cp scripts/pre-commit .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "pre-commit hook installed"

## generate: re-generate mocks via mockery.
generate:
	@mockery

## run: build and start sigild with the dev config, watching ~/workspace.
run: build
	@mkdir -p ~/.local/share/sigild
	$(BIN)/sigild -config dev.toml

## status: query the running daemon via sigilctl.
status: build
	$(BIN)/sigilctl status

## clean: remove build artifacts.
clean:
	@rm -rf $(BIN) cover.out coverage.out
	@# Legacy root-level binaries.
	@rm -f sigild sigilctl sigil-plugin-*

## ---------- Help ----------------------------------------------------------

## help: show this help.
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //' | column -t -s ':'
