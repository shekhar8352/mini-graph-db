# ADR 0001: Go module path

- Status: accepted
- Date: 2026-09-19
- Phase: 0

## Context

The repository lived at module path `mini-graph-db`. That path is not importable with `go get`; other Go applications cannot depend on the packages we will publish under `pkg/` in later phases.

The Git remote is `github.com/shekhar8352/mini-graph-db`. Phase 0 of the roadmap requires renaming the module to an importable path matching that remote. Renaming the GitHub repository itself (for example to `graphdb`) is the owner's call and is not done here.

## Decision

Use the module path **`github.com/shekhar8352/mini-graph-db`**.

All Go imports in this repository follow that path. If the GitHub repository is later renamed, the module path is updated in the same change and this ADR is superseded.

## Alternatives considered

1. **Keep `mini-graph-db`.** Rejected: `go get` cannot fetch it, which blocks the public SDK (Phase 9).
2. **Use a vanity import (`graphdb.dev/...`) immediately.** Rejected: extra DNS/hosting setup; premature before a 1.0 release.
3. **Rename the GitHub repository in this change.** Rejected: that is an owner/ops decision; the current remote already yields a valid module path.

## Consequences

- `go get github.com/shekhar8352/mini-graph-db/...` works once this commit is on the default branch.
- Existing local `replace` directives that pointed at `mini-graph-db` must be updated.
- Binary identity (`graphdb`) is independent of the module path.

## Follow-up

None until a repository rename or a vanity import is desired.
