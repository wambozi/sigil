---
description: Create a feature specification for the Sigil project using spec-driven development.
---

## User Input

```text
$ARGUMENTS
```

You **MUST** consider the user input before proceeding (if not empty).

## Outline

You are creating a feature specification for the Sigil project (`sigild` daemon / `sigilctl` CLI).

1. **Read context**:
   - Read `.specify/memory/constitution.md` for project principles
   - Read `CLAUDE.md` for architecture and build commands
   - Scan `specs/` directory for existing feature numbers

2. **Generate branch and spec structure**:
   - Determine the next feature number by checking existing `specs/` directories
   - Generate a concise short-name from the feature description (2-4 words, kebab-case)
   - Create directory: `specs/<NNN>-<short-name>/`
   - Create spec file: `specs/<NNN>-<short-name>/spec.md`

3. **Write the specification** following the template at `.specify/templates/spec-template.md`:
   - Focus on WHAT and WHY, not HOW
   - Every requirement must be testable
   - Mark genuine ambiguities with `[NEEDS CLARIFICATION: question]` (max 3)
   - Define success criteria that are measurable and technology-agnostic
   - Identify key entities and data involved

4. **Validate against constitution**:
   - Privacy-First: Does this feature respect the local-only data model?
   - Interface-Driven: Will this require new interfaces at package boundaries?
   - DAG Discipline: Where does this fit in the dependency graph?
   - Progressive Trust: If user-facing, does it respect the notification level system?

5. **Report**: Output the spec file path, any clarification questions, and suggest next step (`/speckit.plan`).

## Guidelines

- Specifications are for the Go daemon and CLI, not the shell or OS layers
- Sigil uses pure Go, SQLite (WAL mode), Unix sockets, and structured logging
- All features must be observable via `sigilctl` and the socket API
- Reference existing packages when relevant (event, store, inference, collector, analyzer, notifier, actuator, fleet)
