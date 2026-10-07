# ADR 0010: GQL-lite grammar

- Status: accepted
- Date: 2026-10-07
- Phase: 4

## Context

The shell still parses one legacy statement per line. Phase 4 replaces that language with GQL-lite: pattern matching, projection, aggregation, mutation, transactions, DDL, and DCL. The product decision is already a custom language, familiar to Cypher users, and not an openCypher or Gremlin implementation. The value model, snapshot isolation, and error codes already exist. This ADR freezes the grammar those later tasks implement. The normative text is [spec/query-language.md](../spec/query-language.md). Grammar version is 1.

## Decision

**GQL-lite is a Cypher-shaped clause language over `value.Value`.** Keywords are case-insensitive. Variables, labels, relationship types, property keys, and parameter names are case-sensitive. Strings are double-quoted. Identifiers that clash with a reserved word, or that need a character outside `[A-Za-z_][A-Za-z0-9_]*`, are written in backticks. Parameters are named `$name` only. Statements in a script are separated by `;`. A trailing semicolon is optional. Comments are `--` to end of line and non-nesting `/* */`.

**Reads use snapshot isolation and three-valued predicates.** `WHERE` keeps a row only when the predicate is true. A missing property reads as null. `SET n.prop = null` removes the property, so absence and null are the same stored state. `DISTINCT`, `UNION`, grouping, and unique constraints use `value.Equal`, which treats null as equal to null and `1` as equal to `1.0`. Predicate `=` does not.

**Writes are clause pipelines inside the current transaction.** With no `BEGIN`, each statement auto-commits. Inside an explicit transaction a failed statement rolls back only that statement's writes and leaves the transaction open. `DELETE` of a node that still has edges is `ConstraintViolation`. `DETACH DELETE` removes those edges first. The legacy statement `DELETE NODE` rewrites to `DETACH DELETE`, which keeps today's cascade. `MERGE` does not take a row lock. Two concurrent `MERGE`s can both create a node unless a unique constraint rejects one of them at commit.

**Patterns are relationship-isomorphic.** An edge is used at most once in a match. Nodes may repeat. Variable-length walks are at least one hop and stop at `max_pattern_hops` (default 128). `shortestPath` breaks ties by the lexicographically smallest edge-id sequence.

**Labels and types in patterns are names, not expressions.** A parameter can supply a property value. It cannot supply a label, a relationship type, or a property key. DDL and DCL are in the grammar now. Index and constraint enforcement is Phase 5. Database, user, and grant enforcement is Phase 6 and Phase 7.

## Alternatives considered

1. **Adopt the openCypher grammar.** That fights the product decision and pulls in single-quoted strings, a larger clause set, and compatibility we would then have to preserve. The sketch in the roadmap is the surface we actually need.
2. **Keep growing the line language.** `MATCH person WHERE age > 25` cannot express paths, aggregation, or multi-statement transactions without becoming a second, worse grammar. Legacy statements are rewritten into this AST instead.
3. **Positional parameters and dynamic labels in v1.** Named parameters match the future request map. Dynamic labels need a catalog lookup on every plan and are easy to add later behind a grammar bump.
4. **Row locks inside `MERGE`.** Phase 3 commits by first-committer-wins and does not hold key locks across statements. Uniqueness stays a constraint checked at commit.
5. **Store a null property as a distinct value from absence.** The value model has a null kind for expressions and list elements. Stored properties already omit missing keys. One representation keeps `IS NULL` and `SET n.prop = null` aligned.

## Consequences

- Tasks 4.2 through 4.14 implement this document. They do not extend the grammar without a version bump and an ADR.
- The shell keeps the legacy parser until task 4.12. Bare words in legacy property maps remain strings. In GQL-lite a bare word is a variable.
- Page, WAL, and MVCC format versions stay where Phase 3 left them. This ADR does not change bytes on disk.
- `EXPLAIN` text is stable once the plan printer exists. The logical operator names in the spec are the vocabulary that printer uses.
- Implementers treat the examples in the spec as the first parse corpus.

## Follow-up

- Lexer, AST, parser, semantic analysis, executor, and the shell switch are tasks 4.2–4.14.
- Phase 11 prints the deprecation notice for rewritten legacy statements. The rewrite table in the spec is the contract.
- Named time zones, `EXISTS` subqueries, `FOREACH`, dynamic labels, zero-hop patterns, and parameterized list types are backlog.
