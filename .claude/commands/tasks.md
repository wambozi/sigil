---
description: Generate an actionable task list from a feature's implementation plan.
---

## User Input

```text
$ARGUMENTS
```

You **MUST** consider the user input before proceeding (if not empty).

## Outline

You are generating an executable task list from a Sigil implementation plan.

1. **Locate the plan**:
   - Check current git branch for feature pattern, or use user input
   - Read `specs/<branch-name>/plan.md`
   - Also read `specs/<branch-name>/spec.md` and any supporting artifacts (data-model.md, contracts/)

2. **Load context**:
   - Read `.specify/memory/constitution.md`
   - Read `CLAUDE.md` for build/test commands

3. **Generate tasks** at `specs/<branch-name>/tasks.md`:

   ### Task Format
   ```markdown
   ## Tasks

   ### Phase N: [Phase Name]

   - [ ] **Task N.1**: [Description]
     - Files: `internal/pkg/file.go`
     - Test: `go test -run TestName ./internal/pkg/`
     - Depends: none | Task X.Y

   - [ ] [P] **Task N.2**: [Description] ← [P] marks parallelizable tasks
     - Files: `internal/pkg/other.go`
     - Test: `go test ./internal/pkg/`
     - Depends: Task N.1
   ```

   ### Rules
   - Tasks follow the plan's phase ordering
   - Each task maps to a specific file or small set of files
   - Every task includes its test command
   - Dependencies are explicit
   - Mark independent tasks with `[P]` for safe parallelization
   - Include a verification task at the end of each phase: `make check`
   - Final task: `make coverage` to verify coverage gate

4. **Report**: Output task file path and suggest next step (`/speckit.implement`).
