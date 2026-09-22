# Changelog

All notable work on this repository is recorded here. Task IDs match [ROADMAP.md](../ROADMAP.md).

## Unreleased

### Phase 1 — Data model and typed value system (2026-09-22)

- **1.1** `internal/value.Value` is a tagged union: Null, Bool, Int, Float, String, Bytes, List, Map, Date, DateTime, Duration, Node, Edge, Path. Path is not storable as a property.
- **1.2** Comparisons are three-valued. `= null` is null; `IS NULL` / `IS NOT NULL` are the tests that return bool. `NOT` / `AND` / `OR` follow the same rules. `WHERE` keeps only true rows.
- **1.3** Total order is Null &lt; Bool &lt; Int/Float &lt; String &lt; Bytes &lt; Date &lt; DateTime &lt; Duration &lt; List &lt; Map &lt; Node &lt; Edge &lt; Path. `−0` equals `+0`. Every NaN equals every other NaN and sorts above `+Inf`. Int and float compare exactly, including past 2^53. Durations compare as `(months, days, nanos)`, not elapsed time.
- **1.4** `Equal` and `Hash` follow that order (null equals null; `1` equals `1.0`), for `DISTINCT`, `GROUP BY`, and unique constraints.
- **1.5** `EncodeKey` / `DecodeKey` are memcomparable. Numbers share one encoding (IEEE sign-bit order plus a gap offset for ints that are not exact floats). Strings escape a `0x00` terminator. `EncodeComposite` concatenates keys.
- **1.6** `EncodeRecord` / `DecodeRecord` is a versioned, kind-preserving varint encoding. Record version 1; a newer version is rejected. Float bits, including `−0` and NaN payloads, round-trip.
- **1.7** `Format` and `Parse` cover every kind, including `date(...)`, `datetime(...)`, and `duration(...)`.
- **1.8** Nodes store a sorted unique `[]string` of labels. `Label()` is the first label. The label index maps each label to the set of nodes that carry it.
- **1.9** Property names are interned to `uint32` in the in-memory engine. Properties are `value.Value`. Gob snapshots are format version 1 (label set, intern table, record-encoded properties). Version 0 snapshots still load; a newer version is rejected.
- **1.10** Fuzz tests cover record and key round-trips and key order. A 100k-pair test checks that key order matches `Compare`.

Legacy `WHERE` no longer coerces mismatched types through strings, and numeric comparison is exact (the old `1e-9` tolerance is gone). Shell property cells quote strings.

#### Policy compliance

| Policy | This phase |
|--------|------------|
| P3 Consistency | Types exist. Declared constraints are still Phase 5; the engine rejects unstorable path properties. |
| P10 Change management | Gob snapshot format is version 1. Version 0 still loads. Newer snapshots are rejected with an explicit error. Key and record encodings are versioned. |
| P12 Testing | `go test -race ./internal/value` covers round-trips, a 100k-pair order property, and fuzz seeds. |
| P13 Documentation | `docs/spec/values.md` and [ADR 0002](adr/0002-value-model.md). |
| P1, P2, P4–P9, P11 | Not yet applicable (no page store, transactions, or server). |

### Phase 0 — Foundations, repo hygiene, CI (2026-09-19)

- **0.1** Module path is `github.com/shekhar8352/mini-graph-db` ([ADR 0001](adr/0001-module-path.md)). All imports updated.
- **0.2** `Makefile` targets: `build`, `test` (`-race -count=1`), `lint`, `fmt`, `proto` (placeholder), `bench`, `cover`.
- **0.3** GitHub Actions CI on Go 1.25, Linux and macOS, running `make lint test` with module caching.
- **0.4** `.golangci.yml` enables govet, staticcheck, errcheck, gosimple, ineffassign, unused, misspell, revive, gofmt, and goimports.
- **0.5** `internal/version` injects semver, git commit, and build date via `-ldflags`; `graphdb version` prints them.
- **0.6** `internal/gerr` typed error codes with `Retryable()`. NotFound / Syntax / InvalidArgument wired into graph, query, and traversals.
- **0.7** `internal/logging` wraps `log/slog` (text and JSON). `--log-level` and `--log-format` flags on the CLI; recovery events logged from the shell.
- **0.8** `docs/` skeleton: index, ADR template, ADR 0001, changelog, backlog, dependencies, baseline architecture.
- **0.9** Default snapshot, WAL, and history files live under `./data` (`--data-dir`). Parent directories are created as needed. `.gitignore` covers `/data/`, `/bin/`, and coverage files.
- **0.10** `cmd/graphdb` is a cobra CLI: `graphdb` and `graphdb shell` start the REPL; `graphdb version` prints build info.

#### Policy compliance

| Policy | This phase |
|--------|------------|
| P10 Change management | Module path is the public import identity; on-disk formats unchanged. |
| P12 Testing | CI runs `go test -race` and golangci-lint on Linux and macOS. |
| P13 Documentation | Docs skeleton and README CLI section updated. |
| P1–P9, P11 | Not yet applicable (no networked server, no new durability guarantees). |
