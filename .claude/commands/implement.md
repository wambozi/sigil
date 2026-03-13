---
description: Execute the implementation plan by working through all tasks in tasks.md.
---

## User Input

```text
$ARGUMENTS
```

You **MUST** consider the user input before proceeding (if not empty).

## Outline

You are implementing a Sigil feature by executing the task list.

1. **Locate tasks**:
   - Check current git branch for feature pattern, or use user input
   - Read `specs/<branch-name>/tasks.md`
   - Read `specs/<branch-name>/plan.md` for design context
   - Read `.specify/memory/constitution.md` for principles

2. **Execute tasks in order**:

   For each task:
   a. Read the relevant source files to understand current state
   b. Implement the change following the plan's design
   c. Run the task's test command to verify
   d. Mark the task as complete in `tasks.md`

   ### Implementation Rules
   - Follow the DAG — never import a package that would create a cycle
   - Define interfaces at package boundaries before writing implementations
   - Write tests alongside implementation (or test-first when the plan specifies)
   - Use table-driven tests with `t.Run` subtests
   - Use `openMemory(t)` for store tests, mockery mocks for other boundaries
   - Wrap errors with context: `fmt.Errorf("...: %w", err)`
   - Propagate `context.Context` as first parameter

   ### After each phase:
   ```bash
   go build ./cmd/sigild/ && go build ./cmd/sigilctl/
   go vet ./...
   go test ./...
   ```
   Fix any failures before proceeding to the next phase.

3. **Commit discipline**:
   - Each phase = one commit (unless user specifies otherwise)
   - Commit message: `feat: <description> (closes #N)` when closing issues
   - Never batch commits across phases
   - Never push unless explicitly asked

4. **Final verification**:
   ```bash
   make check
   make coverage
   ```

5. **Report**: Summary of what was implemented, test results, coverage delta.
