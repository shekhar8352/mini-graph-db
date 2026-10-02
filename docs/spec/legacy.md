# Legacy gob snapshot

This is the snapshot written by the old shell. Format versions 0 and 1. The decision record is [ADR 0008](../adr/0008-legacy-import.md). `graphdb migrate` reads it. Nothing in the shell writes it anymore.

`graphdb migrate --from-legacy <snapshot> [--wal <file>] --to <dir>` decodes the snapshot, replays the text WAL when `--wal` is set, and writes a disk database in `<dir>` (`db` and `wal/`). `<dir>` must not already contain `db`.

## Snapshot

The file is one `encoding/gob` value. Integers inside property records are the encoding in [spec/values.md](values.md). The gob struct fields are:

| Field | Meaning |
|-------|---------|
| Version | `0` or `1`. A newer version is refused. |
| Nodes | `ID`, `Label`, `Labels`, `Props` |
| Edges | `ID`, `From`, `To`, `Label`, `Props` |
| NextNode, NextEdge | Next id to allocate. Zero means 1. |
| PropKeys | Intern table. Index is the property-key id. Entry 0 is unused. |

Version 0 stores one label in `Label` and scalar properties (`Kind` `s`, `i`, `f`, or `b` in `Str`, `Int`, `Float`, or `Bool`). Version 1 stores every label in `Labels` and each property as `value.EncodeRecord` in `Record`. A property with a non-empty `Record` uses that and ignores `Kind`.

## Text WAL

The optional WAL is one query line per record, the language the shell still parses. Blank lines and lines that start with `#` are skipped. Lines are replayed in order after the snapshot is loaded. `SAVE` and `LOAD` in that file fail with the same error the shell returns, whose text is: use `graphdb backup`.

A missing `--wal` file is `NotFound`. Omitting `--wal` imports the snapshot alone.
