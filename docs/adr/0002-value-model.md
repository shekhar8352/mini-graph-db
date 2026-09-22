# ADR 0002: Value model, ordering, and encodings

- Status: accepted
- Date: 2026-09-22
- Phase: 1

## Context

Phase 1 introduces the typed value system the rest of the database will share: nulls, ordering, equality, hashing, a memcomparable key encoding, and a compact record encoding. The roadmap fixes most of this. A few points are under-specified or pull in opposite directions, and those choices have to be stable before indexes and the query language depend on them.

Constraints: `internal/value` stays stdlib-only. Key order must match `bytes.Compare` (acceptance test over 100k pairs). Record encoding must round-trip every kind, including NaN payloads and the sign of zero. The legacy gob snapshot must still load.

## Decision

**Tagged struct, not an interface.** `Value` is a struct switched on `Kind`. Composites are slices and strings inside that struct. Callers do not pay an interface dispatch on the comparison hot path.

**Two encodings.** `EncodeKey` is memcomparable and equality-preserving. `EncodeRecord` is kind-preserving and compact (kind byte plus varints; no gob, no reflection). They are not the same bytes. Key format version is 1 and is not a prefix (a prefix would sort first). Record format version is a leading byte; a newer version is rejected.

**Numbers are one ordered family.** Int and float share a key tag. The payload is the IEEE sign-bit inversion of the float, which is the "sign-flipped" total order, plus a 16-bit big-endian gap offset when an int64 is not an exact float64. `1` and `1.0` encode identically. `−0` encodes as `+0`. Every NaN encodes as one payload greater than `+Inf`. Key decoding returns the canonical kind (exact integers come back as int). Record encoding keeps the original kind and the original float bits.

**Ordering equality is not SQL `=`.** `Equal` and `Hash` follow `Compare`, so null equals null and `1` equals `1.0`. That is what `DISTINCT`, `GROUP BY`, and unique constraints need. Predicate `=` is three-valued: if either side is null the result is null, and `IS NULL` is the comparison that returns a bool. Incomparable kinds (anything except null, already handled, and the numeric pair) also yield null rather than a string coercion. `WHERE` keeps only true results.

**NaN and zeros.** All NaNs are equal and greater than every other number. `−0` equals `+0`. There is no comparison epsilon.

**Durations are structural.** A duration is `(months, days, nanos)`. One month is not thirty days. Ordering is lexicographic on those fields. Weeks become days and years become months at parse time; the three stored components are not normalized further.

**DateTime zones participate in equality.** The instant is UTC nanoseconds. An unspecified offset sorts before any recorded offset, including zero, and is not equal to `Z`. Display formatting applies the offset to the clock time.

**Node, edge, and path sort after maps.** The roadmap's total order stopped at Map. These three kinds exist and `ORDER BY` needs a total order, so:

```
Null < Bool < Int/Float < String < Bytes < Date < DateTime < Duration < List < Map < Node < Edge < Path
```

Path is not storable as a property. Node and edge references are.

**Property keys are interned in the in-memory engine.** A stub catalog (not `internal/catalog`, which is Phase 5) assigns `uint32` ids. The gob snapshot gains format version 1: label sets, the intern table, and record-encoded properties. Version 0 snapshots still load. A newer version is rejected.

## Alternatives considered

1. **Separate key tags for int and float.** Simpler, and it matches a literal reading of "sign-flipped ints, IEEE floats" as two encodings. Rejected: cross-type numeric order would disagree with `bytes.Compare`, which the acceptance test forbids. The IEEE trick and the sign bit flip are used inside the shared numeric encoding instead.
2. **Interface or `any` on the hot path.** Rejected: the roadmap asks for a tagged union, and comparison would allocate or indirect.
3. **SQL-style `Equal` where null is not equal to null.** Rejected for `Equal`/`Hash`: unique constraints and `DISTINCT` need a reflexive equality. Three-valued `=` still treats null as unknown.
4. **Drop the zone offset for equality** (compare instants only). Rejected: two datetimes that display differently would collapse in an index. The offset is part of the value; the instant is the primary sort key.
5. **Elapsed-time ordering for durations.** Rejected: months do not have a fixed length, so that order is not total.

## Consequences

- Index keys built in Phase 2 and Phase 5 must use `EncodeKey`. Persisted property bytes must use `EncodeRecord`.
- Changing `Kind` numbers or either encoding is a format bump.
- The legacy query executor compares with `ApplyOp`. Rows no longer match when kinds differ, and numeric comparison is exact.
- Shell property output quotes strings via `Format` (`name="Alice"`).
- Phase 5's catalog replaces the in-memory intern table but should keep `uint32` key ids.

## Follow-up

- Phase 4's grammar should call `ParseDate`, `ParseDateTime`, and `ParseDuration` for the temporal literal functions.
- Assigning null to a property removes it (Phase 4). Phase 1 stores an explicit null if one is written through the typed API.
