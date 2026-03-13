# Tasks: Terminal Session Awareness

**Plan**: `specs/002-terminal-session-awareness/plan.md`
**Generated**: 2026-03-13

## Tasks

### Phase 1: Shell hooks + ingest handler + payload helper

- [x] **Task 1.1**: Add `SessionIDFromPayload` helper to event package
  - Files: `internal/event/payload.go`
  - Test: `go test -run TestSessionIDFromPayload ./internal/event/`
  - Depends: none

- [x] **Task 1.2**: Add tests for `SessionIDFromPayload`
  - Files: `internal/event/payload_test.go`
  - Test: `go test -run TestSessionIDFromPayload ./internal/event/`
  - Depends: Task 1.1

- [x] **Task 1.3**: Extend `ingest` socket handler to accept and pass through `session_id`
  - Files: `cmd/sigild/main.go`
  - Test: `go build ./cmd/sigild/`
  - Depends: Task 1.1

- [x] [P] **Task 1.4**: Update bash shell hook to include `session_id` (shell PID)
  - Files: `scripts/shell-hook.bash`
  - Test: `bash -n scripts/shell-hook.bash`
  - Depends: none

- [x] [P] **Task 1.5**: Update zsh shell hook to include `session_id` (shell PID)
  - Files: `scripts/shell-hook.zsh`
  - Test: `zsh -n scripts/shell-hook.zsh`
  - Depends: none

- [x] **Task 1.6**: Phase 1 verification
  - Test: `make check`
  - Depends: Task 1.2, Task 1.3, Task 1.4, Task 1.5

### Phase 2: Analyzer session-aware grouping

- [x] **Task 2.1**: Add `groupBySession` helper to analyzer patterns
  - Files: `internal/analyzer/patterns.go`
  - Test: `go test -run TestGroupBySession ./internal/analyzer/`
  - Depends: Task 1.1

- [x] **Task 2.2**: Add tests for `groupBySession` (table-driven: with session ID, without, mixed, empty)
  - Files: `internal/analyzer/patterns_test.go`
  - Test: `go test -run TestGroupBySession ./internal/analyzer/`
  - Depends: Task 2.1

- [x] **Task 2.3**: Update `checkBuildFailureStreak` to group by session
  - Files: `internal/analyzer/patterns.go`
  - Test: `go test -run TestBuildFailureStreak ./internal/analyzer/`
  - Depends: Task 2.1

- [x] [P] **Task 2.4**: Update `checkContextSwitchFrequency` to group by session
  - Files: `internal/analyzer/patterns.go`
  - Test: `go test -run TestContextSwitch ./internal/analyzer/`
  - Depends: Task 2.1

- [x] [P] **Task 2.5**: Update `checkSessionLength` to use session ID grouping with gap fallback
  - Files: `internal/analyzer/patterns.go`
  - Test: `go test -run TestSessionLength ./internal/analyzer/`
  - Depends: Task 2.1

- [x] [P] **Task 2.6**: Update `checkIdleGaps` to use session ID grouping with gap fallback
  - Files: `internal/analyzer/patterns.go`
  - Test: `go test -run TestIdleGaps ./internal/analyzer/`
  - Depends: Task 2.1

- [x] **Task 2.7**: Add tests for session-aware pattern checks (per-session streaks, mixed events, backwards compat)
  - Files: `internal/analyzer/patterns_test.go`
  - Test: `go test ./internal/analyzer/`
  - Depends: Task 2.3, Task 2.4, Task 2.5, Task 2.6

- [x] **Task 2.8**: Phase 2 verification
  - Test: `make check`
  - Depends: Task 2.7

### Phase 3: Sessions CLI command + socket handler

- [ ] **Task 3.1**: Add `sessions` socket handler in sigild
  - Files: `cmd/sigild/main.go`
  - Test: `go build ./cmd/sigild/`
  - Depends: Task 1.1

- [ ] **Task 3.2**: Add `sessions` subcommand to sigilctl
  - Files: `cmd/sigilctl/main.go`
  - Test: `go build ./cmd/sigilctl/`
  - Depends: Task 3.1

- [ ] **Task 3.3**: Update socket protocol documentation
  - Files: `docs/socket-protocol.md`
  - Test: none
  - Depends: Task 3.1

- [ ] **Task 3.4**: Phase 3 verification
  - Test: `make check`
  - Depends: Task 3.1, Task 3.2, Task 3.3

### Final

- [ ] **Task 4.1**: Final verification and coverage gate
  - Test: `make check && make coverage`
  - Depends: Task 1.6, Task 2.8, Task 3.4
