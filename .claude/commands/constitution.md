---
description: View or amend the Sigil project constitution.
---

## User Input

```text
$ARGUMENTS
```

You **MUST** consider the user input before proceeding (if not empty).

## Outline

You are viewing or amending the Sigil project constitution at `.specify/memory/constitution.md`.

### If no arguments (view mode):
1. Read and display `.specify/memory/constitution.md`
2. Summarize the current principles and version

### If arguments provided (amend mode):
1. Read the current constitution
2. Analyze the requested change against existing principles
3. Determine version bump:
   - MAJOR: Removing or redefining a principle
   - MINOR: Adding a new principle or section
   - PATCH: Clarifying wording, fixing typos
4. Draft the amendment
5. Present the change to the user for approval before writing
6. Update `Last Amended` date to today
7. Write the updated constitution

### Validation:
- No placeholder tokens remaining (no `[BRACKET_TOKENS]`)
- Principles are declarative and testable (MUST/SHOULD, not "should try to")
- Dates in ISO format (YYYY-MM-DD)
- Version follows semver
