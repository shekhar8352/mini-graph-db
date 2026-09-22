# Value model

This is the typed value system implemented in `internal/value`. Storage, indexes, and the query language share it. The legacy line language still *parses* only booleans, integers, floats, and strings; those literals are stored as values of this model.

## Kinds

A `value.Value` is a tagged union. The zero value is null.

| Kind | Go payload | Storable as a property |
|------|------------|------------------------|
| Null | — | yes |
| Bool | bool | yes |
| Int | int64 | yes |
| Float | float64 (NaN payload and the sign of zero are kept in records) | yes |
| String | UTF-8 bytes, no collation | yes |
| Bytes | raw bytes | yes |
| Date | proleptic Gregorian civil date | yes |
| DateTime | UTC instant as int64 nanoseconds, plus an optional zone offset | yes |
| Duration | months, days, and nanoseconds, stored independently | yes |
| List | ordered values | yes, if every element is |
| Map | string keys to values | yes, if every value is |
| Node | node id (`uint64`) | yes |
| Edge | edge id (`uint64`) | yes |
| Path | alternating node ids and edge ids | **no** |

Path is a query result. A list or map that contains a path is not storable either. The in-memory engine rejects such properties with `InvalidArgument`.

Node ids and edge ids are never reused (see the roadmap). A reference does not imply the entity still exists.

## Literal syntax

`value.Parse` reads one literal. `value.Format` prints one. Keywords below are case-sensitive.

| Kind | Literal | Notes |
|------|---------|--------|
| Null | `null` | |
| Bool | `true` `false` | |
| Int | `0` `-7` `30` | no fraction and no exponent |
| Float | `1.0` `1.5` `-0.0` `1e2` `NaN` `Inf` `-Inf` | a whole float is printed with a decimal point so it parses back as a float |
| String | `"Alice"` `"a\n"` | Go `strconv` quoting |
| Bytes | `bytes("aGVsbG8=")` | standard base64 |
| Date | `date("2025-01-31")` | `YYYY-MM-DD`, optional leading `-` on the year |
| DateTime | `datetime("2025-01-31T12:00:00Z")` | see below |
| Duration | `duration("P1DT2H")` | ISO 8601 |
| List | `[1, "a", null]` | |
| Map | `{name: "Alice", age: 30}` | keys sorted on output |
| Node | `node(1)` | |
| Edge | `edge(2)` | |
| Path | `path(node(1), edge(2), node(3))` | empty path is `path()` |

Map keys that are identifiers may be bare. Anything else, including the words `null`, `true`, and `false`, is a quoted string.

### Date

Years `-9999` through `9999`. The calendar is proleptic Gregorian (year 0 exists and is a leap year). Dates are a day count since `1970-01-01`, which is day 0.

`ParseDate` accepts the inner `YYYY-MM-DD` text without the `date()` wrapper.

### DateTime

The payload is a UTC instant in nanoseconds, about years 1678–2262 (the `time.Time.UnixNano` range).

- `2025-01-31T12:00:00Z` and `2025-01-31T12:00:00+00:00` record offset 0.
- `2025-01-31T12:00:00+05:30` records that offset. The stored instant is UTC (here `06:30Z`). Offsets must be within ±18 hours. Seconds are allowed (`+05:30:00`).
- `2025-01-31T12:00:00` with no zone suffix is the same clock time as a UTC instant, and the offset is **unspecified**. That is not equal to the `Z` form.

`ParseDateTime` accepts the inner timestamp without the `datetime()` wrapper.

### Duration

`ParseDuration` accepts the inner ISO text.

- `P1Y2M3DT4H5M6S`, `P1W`, `PT2H`, `P1DT2H`, `PT1.5S`, `-P1D`.
- A leading `+` or `-` applies to the whole value.
- One year is 12 months. One week is 7 days and may not be combined with `Y`, `M`, or `D`.
- A fraction is allowed only on seconds and is truncated to nanoseconds.
- Months, days, and nanoseconds are **not** normalized into each other. `P1M` is not equal to `P30D`. Ordering is structural `(months, days, nanos)`, not elapsed time.
- Mixed signs are representable (`P1M-2D`). Format prints a leading minus when every component has the same sign, and per-component signs otherwise. Zero is `PT0S`.

## Ordering

`value.Compare` is a total order used by `ORDER BY` and by index keys:

```
Null < Bool < Int/Float < String < Bytes < Date < DateTime < Duration < List < Map < Node < Edge < Path
```

Node, edge, and path were not in the original roadmap list. They sort after maps so `ORDER BY` still has a total order. See [ADR 0002](../adr/0002-value-model.md).

Within a kind:

| Kind | Order |
|------|--------|
| Bool | `false < true` |
| Int / Float | one numeric family; see below |
| String, Bytes | unsigned byte order, not linguistic collation |
| Date | chronological |
| DateTime | UTC instant, then zone: unspecified offset &lt; any recorded offset, then the offset itself |
| Duration | `(months, days, nanos)` |
| List | lexicographic; a proper prefix is smaller |
| Map | entries in key byte order, then values; a proper prefix is smaller |
| Node, Edge | id |
| Path | node, edge, node, …; a proper prefix is smaller |

### Numbers, NaN, and −0

- `1` (int) equals `1.0` (float). Comparison is exact: integers above 2^53 are not rounded through float64, so `2^53+1` is greater than `2^53` and less than `2^53+2`.
- `−0.0` equals `+0.0` and equals int `0`.
- Every NaN equals every other NaN. NaN is greater than every non-NaN number, including `+Inf`.
- There is no epsilon. The legacy `1e-9` numeric tolerance is gone.

## Equality and hashing

`value.Equal` is `Compare == 0`. `value.Hash` matches that: equal values hash the same, including cross-type numbers, both zeros, and every NaN.

This is the equality used by `DISTINCT`, `GROUP BY`, and unique constraints. It is **not** the three-valued predicate `=`. In particular, null equals null under `Equal`, and `1` equals `1.0`.

## Three-valued logic

Predicate operators are `=`, `!=`, `<`, `<=`, `>`, `>=`, `IS NULL`, and `IS NOT NULL`.

- If either side of `=` / `!=` / `<` / `<=` / `>` / `>=` is null, the result is null. `IS NULL` / `IS NOT NULL` are the only tests that return a bool when the subject is null.
- Int and float may be compared with each other. Any other pair of different kinds is incomparable and the predicate returns null (it does not fall back to string comparison).
- `NOT null` is null. `false AND x` is false. `true OR x` is true. `null AND true` is null. `null OR false` is null.
- A `WHERE` clause keeps a row only when the predicate is true. Null and incomparable results do not match. A missing property is not null; it does not match either.

The legacy language still rejects `<`, `>`, `<=`, and `>=` when **both** sides are booleans. `value.Compare` itself orders booleans, so `ORDER BY` can.

## Encodings

Two encodings, not interchangeable.

### Memcomparable key (`EncodeKey` / `DecodeKey`)

`bytes.Compare` on keys matches `Compare`. No encoding of a valid value is a prefix of another, so `EncodeComposite` can concatenate values for a multi-column index key.

| Kind | Encoding |
|------|----------|
| Null | tag `0x01` |
| Bool | tag `0x02`, then `0x00` or `0x01` |
| Int and Float | shared tag `0x03`. Floats use the IEEE sign-bit flip so unsigned byte order matches numeric order. `−0` is canonicalized to `+0` and every NaN to one payload, placed above `+Inf`. An int that is an exact float64 uses that float's bytes and a zero gap offset. An int that falls in a float gap (above 2^53) stores the greatest float below it plus a big-endian offset. |
| String, Bytes | tag, then bytes with `0x00` escaped as `0x00 0xFF`, terminated by `0x00` |
| Date | tag, sign-flipped big-endian day count |
| DateTime | tag, sign-flipped nanoseconds, a flag, and the offset when the flag is set |
| Duration | tag, three sign-flipped int64s |
| List, Map | tag, concatenated element keys, `0x00` terminator. Map keys are string values in sorted order |
| Node, Edge | tag, big-endian uint64 |
| Path | tag, `0x01`+node id and `0x02`+edge id markers, `0x00` terminator |

Tag `0x00` is reserved as a terminator.

Key decoding is canonical for numbers. `DecodeKey(EncodeKey(v))` is `Equal` to `v`, but an exact int64 float comes back as an int, and `−0` comes back as int `0`. Kind is preserved for every other case. `KeyFormatVersion` is 1; key bytes do not carry a version prefix.

### Record (`EncodeRecord` / `DecodeRecord`)

Kind-preserving, not ordered. The first byte is the format version (`1`). A newer version is rejected. Each value then stores its kind byte (the `Kind` constants; do not renumber them) and a payload:

- integers and counts use unsigned varints; signed integers are zigzag-encoded
- floats are little-endian IEEE bits, so NaN payloads and `−0` round-trip
- strings and byte strings are length-prefixed
- lists and maps nest records; map entries are written in key order

The gob snapshot (format version 1) stores property values with this encoding. See the changelog for the version-0 layout, which still loads.

## Legacy coercions

`value.FromAny` accepts `nil` (null), booleans, integers that fit in int64, float32/float64, strings, `[]byte`, `[]any`, and `map[string]any`.

The in-memory engine's `AddNode` / `AddEdge` / `UpdateNode` / `UpdateEdge` take `map[string]any` for the legacy language. A value `FromAny` cannot represent is stored as its printed string. `CreateNode` and `CreateEdge` take `map[string]value.Value` and reject unstorable values.

Property names are interned to `uint32` ids in the in-memory catalog stub. Records store the id. `Node.Label()` returns the first label after the set is sorted and de-duplicated, which keeps single-label nodes compatible with the legacy language.
