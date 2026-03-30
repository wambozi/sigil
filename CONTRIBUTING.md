# Contributing to Sigil

Thanks for your interest in contributing to Sigil.

## Before You Start

1. **Read the constitution.** The project's non-negotiable principles live in
   [`.specify/memory/constitution.md`](.specify/memory/constitution.md). Every
   contribution must conform to them — privacy-first, interface-driven, DAG
   discipline, minimal dependencies.

2. **Open an issue first.** For anything beyond a typo fix, open an issue
   describing what you want to change and why. This saves everyone time if
   the change conflicts with the project's direction.

3. **One logical change per PR.** Don't bundle unrelated fixes. Each PR should
   be reviewable in isolation.

## Development Setup

```bash
git clone https://github.com/wambozi/sigil.git
cd sigil
make build      # build sigild + sigilctl
make check      # fmt + vet + test — must pass before submitting
make coverage   # verify coverage gate (currently 50%)
```

Requires Go 1.24+. No CGo — builds anywhere Go runs.

## Code Standards

- **Effective Go** is the style authority. Not a suggestion — the standard.
- Interfaces at package boundaries. Consumers depend on interfaces, not
  concrete types.
- Table-driven tests with `t.Run`. Mocks via `mockery`, never hand-written.
- Errors wrapped with context: `fmt.Errorf("operation: %w", err)`.
- No dead code. No duplication. No over-engineering.
- `make check` must pass. No exceptions.

See the full Go code standards in the
[constitution](.specify/memory/constitution.md#go-code-standards).

## Package DAG

New packages must fit the dependency graph without creating cycles:

```
event → config → store → inference → collector → notifier → analyzer → actuator → fleet → sync → socket → cmd/sigild
```

`event` is the leaf — zero internal imports. Violating the DAG is a
build-blocking defect.

## Commit Messages

```
feat: short description (closes #N)
fix: short description
refactor: short description
test: short description
docs: short description
```

## License

By contributing, you agree that your contributions will be licensed under the
Apache License 2.0.
