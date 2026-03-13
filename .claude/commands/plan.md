---
description: Generate a technical implementation plan from a feature specification.
---

## User Input

```text
$ARGUMENTS
```

You **MUST** consider the user input before proceeding (if not empty).

## Outline

You are generating a technical implementation plan for a Sigil feature specification.

1. **Locate the active spec**:
   - Check current git branch for a feature branch pattern (`NNN-short-name`)
   - If on main, check user input for a spec reference or list available specs in `specs/`
   - Read the spec file at `specs/<branch-name>/spec.md`

2. **Load context**:
   - Read `.specify/memory/constitution.md` for principles and constraints
   - Read `CLAUDE.md` for architecture, package DAG, and build commands
   - Read relevant existing source files to understand current codebase

3. **Generate the plan** at `specs/<branch-name>/plan.md`:

   ### Pre-Implementation Gates
   - **DAG Gate**: Where do new packages fit? Does this create import cycles?
   - **Interface Gate**: What new interfaces are needed? Who consumes them?
   - **Privacy Gate**: Does any data leave the machine? Is it user-controlled?
   - **Simplicity Gate**: Is this the minimum complexity needed?

   ### Technical Design
   - New/modified packages and their responsibilities
   - Interface definitions (Go interface signatures)
   - Data model changes (SQLite schema, event types)
   - Socket API additions (new handler methods)
   - Config changes (new TOML sections/fields)

   ### Implementation Phases
   - Break work into ordered phases respecting the dependency DAG
   - Each phase should be one commit
   - Include build verification commands per phase

   ### Testing Strategy
   - Table-driven tests with `t.Run`
   - Mock boundaries (which interfaces get mocks)
   - Integration test approach (real SQLite via `openMemory(t)`)

4. **Generate supporting artifacts** (as needed):
   - `specs/<branch-name>/data-model.md` — schema changes
   - `specs/<branch-name>/contracts/` — socket API contracts

5. **Report**: Output the plan file path and suggest next step (`/speckit.tasks`).

## Constraints

- All Go code follows Effective Go and the project's existing patterns
- New packages must fit the existing DAG (event → config → store → ... → cmd/sigild)
- SQLite single-writer constraint must be respected
- Context propagation is mandatory for all blocking operations
- `make check` must pass after every phase
