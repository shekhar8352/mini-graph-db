# GQL-lite

Grammar version **1**. The decision record is [ADR 0010](../adr/0010-gql-lite.md). Values, ordering, and predicate rules come from [spec/values.md](values.md). Transactions come from [spec/transactions.md](transactions.md).

This document is the language. The shell still executes the legacy line language until task 4.12 rewrites those statements into this grammar. File extension for a script is `.gql`.

## Source text

A script is UTF-8. A byte sequence that is not UTF-8 is `Syntax` at line 1, column 1.

Line endings are `\n` or `\r\n`. The lexer treats `\r\n` as one break. Lines and columns in errors are **1-based**. The column is the byte offset within the line.

Whitespace is space, tab, and a line break. It separates tokens and does not affect meaning.

Comments do not nest:

- `--` starts a comment that runs through the end of the line.
- `/*` starts a comment that runs through the next `*/`.

A comment starts only outside a string and outside a backtick identifier. An unclosed `/*` is `Syntax`.

Statements are separated by `;`. A final `;` may be omitted. A run of extra `;` is an empty statement and is ignored. A script that contains only whitespace and comments succeeds and yields no results.

## Case

Keywords, built-in function names, built-in procedure names, `true`, `false`, `null`, `NaN`, and `Inf` are case-insensitive.

These are case-sensitive: variables, labels, relationship types, property keys, parameter names, aliases, index names, constraint names, database names, user names, and role names. `Person` and `person` are different labels.

## Lexical grammar

```
ident        = letter { letter | digit | "_" } ;
letter       = "A".."Z" | "a".."z" | "_" ;
digit        = "0".."9" ;
hexDigit     = digit | "A".."F" | "a".."f" ;

quotedIdent  = "`" { quotedChar } "`" ;
quotedChar   = "``" | any UTF-8 character except "`" and a line break ;
name         = ident | quotedIdent ;

integer      = decimal | hexInteger ;
decimal      = digit { digit } ;
hexInteger   = "0" ("x" | "X") hexDigit { hexDigit } ;
float        = digits "." digits [ exponent ]
             | digits exponent ;
digits       = digit { digit } ;
exponent     = ("e" | "E") [ "+" | "-" ] digits ;

string       = `"` { stringChar } `"` ;
parameter    = "$" ( ident | quotedIdent ) ;
```

An unquoted `ident` whose text matches a reserved word, ignoring case, is that keyword. A backtick identifier is never a keyword. Its text is the characters inside the backticks, with each ` `` ` pair meaning one backtick. An empty backtick pair is `Syntax`.

`$` glues to the following name. `$from` is one parameter token. A keyword after `$` is a parameter name, so `$from` is legal even though `FROM` is reserved.

### Numbers

A decimal or hex literal that does not fit in `int64` is `Syntax`. Hex is an integer. `0x` with no digits is `Syntax`.

A float literal is `float64`. Overflow becomes `+Inf` or `-Inf`. `NaN`, `Inf`, and the sign of `Inf` are keywords, so `-Inf` is unary minus on `Inf`.

The lexer splits `.` as follows, left to right:

- Two dots form the token `..`.
- A dot followed by a digit continues a float when the current token is an integer (`1.5`, `1.5e2`).
- Any other dot is the property token `.`.

`1..2` is an integer, `..`, and an integer. `*1..3` uses that split.

### Strings

Strings use Go's double-quoted escapes: `\\`, `\"`, `\n`, `\t`, `\r`, `\a`, `\b`, `\f`, `\v`, `\xHH`, `\uHHHH`, `\UHHHHHHHH`, and octal `\NNN`. A raw line break inside a string is `Syntax`. An unknown escape is `Syntax`. There is no single-quoted string.

### Multi-character operators

The lexer prefers the longest operator:

`..`  `<>`  `!=`  `<=`  `>=`  `::`  `->`  `<-`  `+=`

Then single-character tokens: `( ) [ ] { } , : . ; + - * / % ^ = < > | $`

`-->` is the two tokens `-` and `->`. `<--` is `<-` and `-`. A pair of dashes starts a `--` comment, except when those dashes are the prefix of `-->`: that prefix is minus and then `->`. The undirected shorthand is written `-[]-`, or as two minuses with whitespace between them (`- -`). Adjacent `--` is a comment.

## Reserved words

An unquoted reserved word is a keyword. It is accepted as an identifier in exactly these positions:

- immediately after `.`
- immediately after `:`
- a map key
- immediately after `AS`
- immediately after `YIELD`
- the callee of a call, when `(` follows and the word names a built-in function

```
ADMIN ALL ANALYZE AND ANY AS ASC BEGIN BY CALL CASE CHANGE COMMIT CONFIRM
CONSTRAINT CONSTRAINTS CONTAINS CREATE DATABASE DATABASES DELETE DENY DESC
DETACH DISTINCT DROP EDGE ELSE END ENDS EXPIRES EXPLAIN FALSE FIRST FOR FROM
GRANT IN INDEX INDEXES INF IS KEYS LABELS LAST LIMIT MATCH MERGE NAN NONE NOT
NULL NULLS ON OPTIONAL OR ORDER PASSWORD PRIVILEGES PROFILE PROPERTY READ
REDUCE REMOVE REQUIRED RETURN REVOKE ROLE ROLES ROLLBACK SET SHOW SINGLE SKIP
STARTS STATS TERMINATE THEN TO TOKEN TRANSACTION TRANSACTIONS TRUE TYPES UNION
UNIQUE UNWIND USE USER USERS VACUUM WHEN WHERE WITH WRITE XOR YIELD
```

A variable whose name is reserved is written in backticks: `` (`match`) ``.

## Types named in the grammar

A type name is an identifier, compared case-insensitively, and only in a type position (`IS ::` and `REQUIRE … IS ::`):

```
NULL BOOL INT FLOAT STRING BYTES DATE DATETIME DURATION LIST MAP NODE EDGE PATH
```

`NULL` in that position is the keyword. The others are ordinary identifiers. `IS :: INT` is true only for kind int. `1.0 IS :: INT` is false. Kinds are not parameterized: there is no `LIST OF INT` in this version.

## Grammar

The grammar below uses tokens. `{ x }` means zero or more. `[ x ]` means optional. `|` separates alternatives. Quoted text is a keyword or an operator.

```
script          = { statement [ ";" ] } ;

statement       = query
                | beginStmt | "COMMIT" | "ROLLBACK"
                | "EXPLAIN" query | "PROFILE" query
                | "VACUUM" | "ANALYZE"
                | "USE" name
                | "TERMINATE" "TRANSACTION" integer
                | showStmt | ddl | dcl ;

beginStmt       = "BEGIN" [ "READ" "ONLY" ] ;

showStmt        = "SHOW" ( "INDEXES" | "CONSTRAINTS" | "LABELS"
                | "EDGE" "TYPES" | "PROPERTY" "KEYS" | "STATS"
                | "DATABASES" | "USERS" | "ROLES"
                | "PRIVILEGES" "FOR" name
                | "TRANSACTIONS" ) ;

query           = singleQuery { unionOp singleQuery } ;
unionOp         = "UNION" [ "ALL" | "DISTINCT" ] ;
singleQuery     = clause { clause } ;
clause          = matchClause | unwindClause | withClause | callClause
                | createClause | mergeClause | setClause | removeClause
                | deleteClause | returnClause ;

matchClause     = [ "OPTIONAL" ] "MATCH" pattern [ "WHERE" expr ] ;
unwindClause    = "UNWIND" expr "AS" name ;
withClause      = "WITH" returnBody [ "WHERE" expr ] ;
returnClause    = "RETURN" returnBody ;
returnBody      = [ "DISTINCT" ] projection
                  [ "ORDER" "BY" sortItem { "," sortItem } ]
                  [ "SKIP" expr ] [ "LIMIT" expr ] ;
projection      = "*" | projItem { "," projItem } ;
projItem        = expr [ "AS" name ] ;
sortItem        = expr [ "ASC" | "DESC" ] [ "NULLS" ( "FIRST" | "LAST" ) ] ;

callClause      = "CALL" procName "(" [ expr { "," expr } ] ")"
                  [ "YIELD" yieldItem { "," yieldItem } ] ;
procName        = name { "." name } ;
yieldItem       = name [ "AS" name ] ;

createClause    = "CREATE" pattern ;
mergeClause     = "MERGE" path { mergeAction } ;
mergeAction     = "ON" ( "CREATE" | "MATCH" ) setClause ;
setClause       = "SET" setItem { "," setItem } ;
setItem         = name "." name "=" expr
                | name "=" expr
                | name "+=" expr
                | name labelList ;
removeClause    = "REMOVE" removeItem { "," removeItem } ;
removeItem      = name "." name | name labelList ;
deleteClause    = [ "DETACH" ] "DELETE" expr { "," expr } ;

pattern         = path { "," path } ;
path            = [ name "=" ] pathArm ;
pathArm         = node { rel node }
                | "shortestPath" "(" node rel node ")"
                | "allShortestPaths" "(" node rel node ")" ;
node            = "(" [ name ] [ labelList ] [ map ] ")" ;
labelList       = ":" labelAlt { ":" labelAlt } ;
labelAlt        = name { "|" name } ;
rel             = inbound | outbound | either ;
outbound        = "-" relBody "->" | "-" "->" ;
inbound         = "<-" relBody "-" | "<-" "-" ;
either          = "-" relBody "-" | "-" "-" ;
relBody         = "[" [ name ] [ typeList ] [ length ] [ map ] "]" ;
typeList        = ":" name { "|" name } ;
length          = "*" [ integer ] [ ".." [ integer ] ] | "*" ".." integer ;

ddl             = createIndex | "DROP" "INDEX" name
                | createConstraint | "DROP" "CONSTRAINT" name ;
createIndex     = "CREATE" "INDEX" name "FOR" indexFor "ON" "(" propRef { "," propRef } ")" ;
indexFor        = "(" name ":" name ")"
                | "(" ")" "-" "[" name ":" name "]" "-" "(" ")" ;
propRef         = name "." name ;
createConstraint= "CREATE" "CONSTRAINT" name "FOR" indexFor
                  "REQUIRE" name "." name "IS" constraintTail ;
constraintTail  = "UNIQUE" | "NOT" "NULL" | "::" typeName ;

dcl             = "CREATE" "DATABASE" name
                | "DROP" "DATABASE" name [ "CONFIRM" ]
                | "CREATE" "USER" name "SET" "PASSWORD" string [ "CHANGE" "REQUIRED" ]
                | "ALTER" "USER" name "SET" "PASSWORD" string [ "CHANGE" "REQUIRED" ]
                | "DROP" "USER" name
                | "CREATE" "ROLE" name
                | "DROP" "ROLE" name
                | "GRANT" "ROLE" name "TO" name
                | "REVOKE" "ROLE" name "FROM" name
                | "GRANT" privilege "ON" "DATABASE" name "TO" name
                | "REVOKE" privilege "ON" "DATABASE" name "FROM" name
                | "DENY" privilege "ON" "DATABASE" name "TO" name
                | "CREATE" "TOKEN" "FOR" "USER" name "EXPIRES" "IN" expr
                | "REVOKE" "TOKEN" integer ;
privilege       = "READ" | "WRITE" | "ADMIN" ;
```

Expressions:

```
expr        = orExpr ;
orExpr      = xorExpr { "OR" xorExpr } ;
xorExpr     = andExpr { "XOR" andExpr } ;
andExpr     = notExpr { "AND" notExpr } ;
notExpr     = { "NOT" } comparison ;
comparison  = addExpr [ compTail ] ;
compTail    = compOp addExpr
            | "IS" [ "NOT" ] "NULL"
            | "IS" [ "NOT" ] "::" typeName
            | "IN" addExpr
            | "STARTS" "WITH" addExpr
            | "ENDS" "WITH" addExpr
            | "CONTAINS" addExpr ;
compOp      = "=" | "<>" | "!=" | "<" | "<=" | ">" | ">=" ;

addExpr     = mulExpr { ( "+" | "-" ) mulExpr } ;
mulExpr     = unaryExpr { ( "*" | "/" | "%" ) unaryExpr } ;
unaryExpr   = { "+" | "-" } powExpr ;
powExpr     = postfix [ "^" unaryExpr ] ;
postfix     = primary { "." name | "[" expr "]" | "[" expr ".." [ expr ] "]" } ;
primary     = literal | parameter | name | "(" expr ")"
            | listLit | map
            | "[" name "IN" expr [ "WHERE" expr ] [ "|" expr ] "]"
            | quantifier | reduce | caseExpr | functionCall ;

literal     = "NULL" | "TRUE" | "FALSE" | "NAN" | "INF"
            | integer | float | string ;
listLit     = "[" [ expr { "," expr } ] "]" ;
map         = "{" [ mapEntry { "," mapEntry } ] "}" ;
mapEntry    = ( name | string ) ":" expr ;
quantifier  = ( "ANY" | "ALL" | "NONE" | "SINGLE" )
              "(" name "IN" expr "WHERE" expr ")" ;
reduce      = "REDUCE" "(" name "=" expr "," name "IN" expr "|" expr ")" ;
caseExpr    = "CASE" expr { "WHEN" expr "THEN" expr } [ "ELSE" expr ] "END"
            | "CASE" { "WHEN" expr "THEN" expr } [ "ELSE" expr ] "END" ;
functionCall= name "(" [ "DISTINCT" ] [ expr { "," expr } ] ")" ;
```

`^` is right-associative and binds tighter than unary minus, so `-2^2` is `-(2^2)`. Every other binary operator is left-associative. A comparison chain such as `a < b < c` is `Syntax`. `DISTINCT` inside a call is legal only on an aggregate.

### Precedence, tightest first

| Level | Forms |
|------:|-------|
| 1 | property `.`, index `[]`, slice `[ .. ]`, call `()` |
| 2 | `^` |
| 3 | unary `+` `-` |
| 4 | `*` `/` `%` |
| 5 | `+` `-` |
| 6 | `=` `<>` `!=` `<` `<=` `>` `>=` `IN` `STARTS WITH` `ENDS WITH` `CONTAINS` `IS NULL` `IS ::` |
| 7 | `NOT` |
| 8 | `AND` |
| 9 | `XOR` |
| 10 | `OR` |

## Names and scope

A variable is bound by a node or relationship pattern, `UNWIND … AS`, a projection alias, `YIELD … AS`, a list comprehension, a quantifier, or `REDUCE`. The comprehension, quantifier, and `REDUCE` variables exist only inside that expression. They hide an outer variable of the same name.

`WITH` and `RETURN` end the current scope. After `WITH`, only the projected names are visible. `*` projects every name bound in the current scope, in binding order.

Using a name that is not bound is `Semantic`. Binding the same name twice in one scope, including two aliases with the same text, is `Semantic`. Mentioning a name that is already bound in a pattern is a join: the pattern must match that value. `MATCH (a)-[]->(a)` is a self-loop.

`SKIP` and `LIMIT` expressions do not refer to row variables. They may use literals, parameters, and functions of those. A row variable there is `Semantic`.

## Clause order

Clauses of one `singleQuery` run left to right. A later `MATCH` sees bindings and writes from earlier clauses in the same statement.

- A query has at least one clause.
- A query that writes and has no `RETURN` yields zero rows and a summary.
- A query that only reads ends with `RETURN`, or is a single `CALL` whose result columns are the procedure columns. Any other read-only shape is `Semantic`.
- A `CALL` that is followed by another clause has a `YIELD`. A `CALL` that is the whole statement returns the procedure columns.
- `RETURN` is the last clause of its `singleQuery`.
- `WITH` is not the last clause.
- Each side of `UNION` ends with `RETURN`. The two sides have the same number of columns, and the column names match in order, case-sensitively. The result names are the left-hand names. A mismatch is `Semantic`.
- `UNION` and `UNION DISTINCT` drop duplicate rows under `value.Equal`. `UNION ALL` keeps them.
- `ON CREATE` and `ON MATCH` each appear at most once on a `MERGE`.

Aggregates (`count`, `sum`, `avg`, `min`, `max`, `collect`, `percentileDisc`, `stdev`, and `count(*)`) appear only in a `RETURN` or `WITH` projection. If any projected expression is an aggregate, every other projected expression is either an aggregate or a grouping key. An aggregate nested inside another aggregate is `Semantic`. `WHERE` does not contain an aggregate; filter the aggregated rows with `WITH` and then `WHERE`. `ORDER BY` may use a projection alias, a grouping key, or an aggregate that is already in the projection.

## Patterns

A node pattern `(n:A:B|C {k: expr})` binds `n` to a node that has label `A` and at least one of `B` or `C`. Each colon-separated group is required. Names inside one group are alternatives. The map is a predicate on a `MATCH` and on the match step of `MERGE`: each listed property must satisfy predicate `=`. On `CREATE`, and on the create step of `MERGE`, the map is the initial properties.

An edge has one type. `:KNOWS|WORKS` matches either type. `:KNOWS:WORKS` is `Syntax`. A `CREATE` or `MERGE` relationship has exactly one type. A `MATCH` relationship may omit the type and then matches every type.

A relationship variable on a single hop is that edge. On a variable-length hop it is the list of edges in walk order. `size(k)` is the hop count. `k.since` on that list is `Semantic`.

Direction:

| Written | Matches |
|---------|---------|
| `-[]->` or `-->` | stored `from → to` |
| `<-[]-` or `<--` | stored `to → from` |
| `-[]-` or `- -` | either stored direction. Adjacent `--` is a comment |

`startNode` and `endNode` follow the stored edge. A path's `nodes` list follows walk order.

Hop counts are inclusive. No star means exactly one hop. `*n` means exactly `n`. `*min..max`, `*min..`, and `*..max` are ranges. A bare `*` is `*1..`. The lower bound is at least 1. `*0` and `*0..n` are `Semantic`. When `min` and `max` are both written, `min <= max`, or the pattern is `Semantic`. An omitted upper bound, or an upper bound above the session limit, stops at `max_pattern_hops`. The default limit is 128. A walk that hits the limit is `ResourceExhausted`.

Within one `MATCH`, each stored edge is used at most once. Nodes may repeat. Two patterns separated by a comma are a cross product, and the isomorphism rule applies to the whole `MATCH`.

`shortestPath` and `allShortestPaths` take a single relationship chain between two nodes. Both endpoint variables are already bound. The relationship may be variable-length. A property map on that relationship is `Semantic`. Among paths with the smallest hop count, `shortestPath` picks the lexicographically smallest sequence of edge ids. `allShortestPaths` returns every path of that hop count, ordered by the same sequence. No path yields null for `shortestPath` and an empty list for `allShortestPaths`. The same two names are functions on a pair of nodes (any outgoing type, same tie-break).

`OPTIONAL MATCH` yields one row of nulls for the names it would have bound when nothing matches. Names that were already bound stay bound. A failed optional match does not drop the incoming row.

## Expressions

### Three-valued logic

`AND`, `OR`, `XOR`, `NOT`, comparisons, `IN`, `STARTS WITH`, `ENDS WITH`, `CONTAINS`, and `IS ::` return `true`, `false`, or null.

- `NOT null` is null. `false AND x` is false. `true OR x` is true. `null AND true` is null. `null OR false` is null.
- `AND` and `OR` evaluate the left operand first and skip the right operand when the result is already fixed. `false AND (1/0)` is false. `null AND x` still evaluates `x`, and the result is false when `x` is false. `XOR` evaluates both sides.
- A comparison with a null operand is null. Different kinds are null, except int and float, which compare as in [spec/values.md](values.md).
- `<`, `<=`, `>`, and `>=` on booleans are null. `=` and `!=` on booleans are booleans. `ORDER BY` still uses the total order, where `false < true`.
- `IS NULL` and `IS NOT NULL` are the tests that return a boolean for null.
- `IS ::` on null is true only for `NULL`.
- `WHERE` keeps a row when the predicate is true.

`<>` and `!=` are the same operator.

### Equality used for sets

`DISTINCT`, grouping, `UNION`, `IN`'s element test, simple `CASE`, and unique constraints use `value.Equal` for the membership test described below. Predicate `=` is three-valued and is what `WHERE a = b` uses.

`IN` over a list: the result is true when predicate `=` is true for any element; otherwise null when any comparison is null; otherwise false. `x IN null` is null. A right-hand side that is neither a list nor null is `InvalidArgument`.

`STARTS WITH`, `ENDS WITH`, and `CONTAINS` require strings. Either side null yields null. Any other kind is `InvalidArgument`. They compare UTF-8 bytes, case-sensitively. The empty string is a prefix, suffix, and substring of every string.

### Arithmetic and concatenation

`+` on two numbers is addition. Int plus int stays int; overflow is `InvalidArgument`. If either side is float, the result is float. `+` on two strings is concatenation. `+` on two lists is concatenation. Any other pair is `InvalidArgument`. A null operand yields null.

`-` on numbers is subtraction, with the same kind and overflow rules. `-` on two durations is component-wise subtraction.

`*` and `/` on numbers follow the same kind rules. Integer `/` truncates toward zero. Division by zero is `InvalidArgument`. `%` requires two ints, truncates toward zero, and the remainder takes the sign of the dividend. A zero divisor is `InvalidArgument`.

`^` always returns a float. `0^` of a negative exponent is `InvalidArgument`.

Unary minus of `int64` minimum is `InvalidArgument`.

### Index and slice

`list[i]` uses a 0-based int. A negative index counts from the end. An index that lands outside the list is null. `list[i..j]` is a half-open slice and clamps to the list. An omitted `j` means the length. A non-int index is `InvalidArgument`.

`map[key]` and `nodeOrEdge[key]` read a property by string key. A missing key is null. An int key on a map, node, or edge is `InvalidArgument`.

### Collections

`[x IN list WHERE pred | expr]` filters, then maps. Either the `WHERE` or the `|` may be omitted. `[x IN list WHERE pred]` keeps the elements that pass. The variable is local.

`ANY`, `ALL`, `NONE`, and `SINGLE` bind a local variable. `ALL` and `NONE` are true for an empty list. `ANY` and `SINGLE` are false for an empty list. A null predicate counts as not true. `SINGLE` is true when exactly one element makes the predicate true.

`REDUCE (acc = init, x IN list | expr)` starts from `init` and folds left to right. `acc` and `x` are local.

### CASE

Searched `CASE` evaluates `WHEN` predicates in order and returns the first `THEN` whose predicate is true. Simple `CASE expr WHEN value` compares with predicate `=`, so null does not match null. The scrutinee is evaluated once. No matching arm and no `ELSE` yields null.

### Property maps

`{name: expr, "other": expr}` builds a map. Duplicate keys are `Semantic`. Output order is not significant; `value.Equal` compares maps by key.

## Reading a graph value

`id` returns an int. Allocated ids are in `1 .. 2^63-1`. `labels` returns the label set as a list of strings in byte order. `type` returns the relationship type. `properties` returns a map copy. A missing property reads as null.

Assigning null with `SET n.k = null` removes `k`. The store does not keep a null property.

A path value is the alternating node and edge ids of one walk. Paths are results. A path is not a legal property value. A list or map that contains a path is not a legal property value either. Storing one is `InvalidArgument`.

## Mutation

Clauses see the transaction snapshot plus earlier writes of that transaction, including earlier clauses of the same statement.

`CREATE` inserts each unbound node and each relationship. A relationship's endpoints are created in the same clause when their variables are unbound. A bound node is reused: labels in the pattern are added, and map keys are set. Created nodes start with the listed labels, which may be empty. Ids are never reused.

`MERGE`, for each input row:

1. Look for a full match of the path.
2. If matches exist, bind the unbound names from the match whose edge-id sequence is smallest, then run `ON MATCH`.
3. If none exist, create each unbound node and relationship, reuse bound nodes that satisfy their predicates, then run `ON CREATE`.
4. A bound node or relationship that fails its predicates is `InvalidArgument`. The statement does not invent a second entity for a bound name.

The path has one chain, no comma, no variable length, and no `shortestPath`. Anything else is `Semantic`. `MERGE` takes no row lock. A unique constraint is checked at commit of the enclosing transaction. Two sessions can both create; one then receives `ConstraintViolation`.

`SET n.k = expr` sets one property. `SET n = map` replaces every property and leaves labels alone. `SET n = null` removes every property. `SET n += map` merges keys. `SET n:A:B` adds labels. Adding a label the node already has is a no-op.

`REMOVE n.k` removes one property. `REMOVE n:A` removes a label. A missing property or label is a no-op. A node may be left with zero labels.

`DELETE` of an edge deletes that edge. `DELETE` of a node deletes it when no edge touches it. A node that still has an edge is `ConstraintViolation`. `DETACH DELETE` deletes the incident edges and then the node. `DELETE null` does nothing. Any other kind is `InvalidArgument`. Deleting an entity already deleted in this statement does nothing.

A write statement is atomic inside the enclosing transaction. On `Conflict`, `ConstraintViolation`, or any other error, that statement's writes are discarded. An explicit transaction stays open. In auto-commit, the statement's transaction is rolled back.

A `BEGIN READ ONLY` transaction rejects a writing statement with `InvalidArgument`.

## Transactions and scripts

`BEGIN`, `BEGIN READ ONLY`, `COMMIT`, and `ROLLBACK` are the transaction statements from [spec/transactions.md](transactions.md). `BEGIN` while a transaction is open, and `COMMIT` or `ROLLBACK` with none open, are `InvalidArgument`. `BEGIN READ` without `ONLY` is `Syntax`.

A script runs statements in order and produces one result per non-empty statement. An error stops the script. Statements that already auto-committed stay committed. Inside an explicit transaction, earlier statements of that transaction remain, and the failed statement's writes do not.

`EXPLAIN query` returns one column, `plan`, and does not run the query. `PROFILE query` runs it and returns the query's columns plus operator statistics. The plan printer names these logical operators: `NodeScan`, `LabelScan`, `IndexSeek`, `Expand`, `Filter`, `Project`, `Aggregate`, `Sort`, `Limit`, `Skip`, `Distinct`, `Union`, `Unwind`, `Optional`, `Apply`, `Create`, `Merge`, `SetProps`, `SetLabels`, `Remove`, `Delete`, `ShortestPath`. DDL and DCL print as a single leaf of that statement's name. The text format is fixed by the plan printer and is stable across runs.

## Result of a query

A result has column names, rows of `value.Value`, a summary, and notifications.

Returning a node or an edge returns that entity reference, not an inlined property map. Callers project `id(n)`, `labels(n)`, `properties(n)`, or `n.name`.

The summary counts `nodesCreated`, `nodesDeleted`, `edgesCreated`, `edgesDeleted`, `propertiesSet`, `labelsAdded`, `labelsRemoved`, `indexesAdded`, `indexesRemoved`, `constraintsAdded`, `constraintsRemoved`. Setting a property counts even when the new value equals the old one. Removing a missing property does not count.

A notification is `{code, message}`. The legacy rewrite adds `DeprecatedSyntax` and the statement still runs. Phase 11 is what prints that notice in the shell.

`ORDER BY` is a stable sort under `value.Compare`. The default direction is `ASC`. The default null placement is `NULLS FIRST`, because null is the smallest value in the total order. `SKIP` and `LIMIT` take a non-negative int. A negative value, a float, or null is `InvalidArgument`. `SKIP` past the end yields no rows. `LIMIT 0` yields no rows.

## Parameters

A parameter is `$` plus a name. The caller supplies `map[string]value.Value`. Names are case-sensitive. A referenced name that was not supplied is `InvalidArgument`, reported before any write. A supplied name that the statement does not read is ignored.

Parameters are values. They do not stand in for labels, relationship types, property keys, or statement keywords.

## Built-in functions

A null argument yields null, except where the row says otherwise. The wrong number of arguments is `Semantic`. The wrong kind is `InvalidArgument`. Names are case-insensitive.

### Strings

| Function | Result |
|----------|--------|
| `toUpper(s)`, `toLower(s)` | Unicode case mapping |
| `trim(s)` | strip leading and trailing Unicode spaces |
| `split(s, sep)` | list of strings. An empty separator is `InvalidArgument`. `split("", ",")` is `[""]` |
| `substring(s, start)`, `substring(s, start, len)` | 0-based code points. A negative start or length is `InvalidArgument`. The slice clamps at the end |
| `replace(s, search, repl)` | replace every non-overlapping `search`. An empty `search` is `InvalidArgument` |
| `startsWith(s, prefix)`, `endsWith(s, suffix)`, `contains(s, sub)` | same rules as the infix operators |
| `toString(v)` | `value.Format`. `toString(null)` is null |
| `size(s)` | code-point length. Also `size(bytes)`, `size(list)`, and `size(map)` |

### Numbers

| Function | Result |
|----------|--------|
| `abs(n)` | same kind. `abs` of `int64` minimum is `InvalidArgument` |
| `ceil(n)`, `floor(n)`, `round(n)` | int stays int. Float stays float and moves to a whole number. `round` ties away from zero |
| `sqrt(n)` | float. A negative argument is `InvalidArgument` |
| `pow(base, exp)` | float. Same restrictions as `^` |
| `sign(n)` | int `-1`, `0`, or `1`. `sign` of NaN is null |
| `rand()` | float in `[0, 1)`. No arguments. A new value on every call |

`rand` and `now` are not stable across runs. Golden tests do not compare their values.

### Lists and maps

| Function | Result |
|----------|--------|
| `range(start, end)`, `range(start, end, step)` | inclusive int range. Step `0` is `InvalidArgument`. The wrong sign yields `[]` |
| `head(list)` | first element, or null if empty |
| `tail(list)` | every element after the first; empty stays empty |
| `last(list)` | last element, or null if empty |
| `reverse(list)` | reversed list |
| `keys(map)` | keys in byte order. `keys(node)` and `keys(edge)` are property names in that order |
| `values(map)` | values in key order |

### Temporal

`date(s)` parses `YYYY-MM-DD` as in the value spec. `datetime(s)` parses a timestamp literal from that spec. `duration(s)` parses an ISO-8601 duration from that spec. `now()` returns the current UTC instant with offset 0.

`date(datetime)` is the UTC civil date of that instant. Component functions:

| On | Functions |
|----|-----------|
| date | `year`, `month`, `day`, `quarter`, `dayOfWeek` (Monday is 1, Sunday is 7) |
| datetime | the date functions, plus `hour`, `minute`, `second`, `millisecond`, `microsecond`, `nanosecond`, `offsetSeconds` (null when the offset is unspecified) |
| duration | `months`, `days`, `nanoseconds` are the stored fields. `hours`, `minutes`, and `seconds` are derived from the nanosecond field only |

There are no named time zones. An offset is a fixed shift.

Addition:

- `date + duration` adds the months, then the days. A non-zero nanosecond field is `InvalidArgument`. The day clamps to the last day of the resulting month: `date("2025-01-31") + duration("P1M")` is `2025-02-28`.
- `datetime + duration` does that calendar arithmetic in the stored offset, or in UTC when the offset is unspecified, then adds nanoseconds, and keeps the offset.
- `date - date` is a duration of days.
- `datetime - datetime` is a duration of nanoseconds. A difference that does not fit in `int64` nanoseconds is `InvalidArgument`.
- `duration + duration` and `duration - duration` are component-wise. Overflow is `InvalidArgument`.
- `duration * int` and `int * duration` scale every component. `duration / int` truncates each component toward zero. A zero divisor is `InvalidArgument`. `duration * float` is `InvalidArgument`.

### Types

| Function | Result |
|----------|--------|
| `toInt(v)` | int stays. A finite float truncates toward zero when the result fits. A string must be an integer literal. Booleans are `InvalidArgument` |
| `toFloat(v)` | int, float, or a float literal string |
| `toBool(v)` | bool, or the strings `true` and `false` ignoring case |
| `coalesce(v, …)` | the first non-null argument, or null. At least one argument |
| `typeOf(v)` | lowercase kind name: `null`, `bool`, `int`, `float`, `string`, `bytes`, `date`, `datetime`, `duration`, `list`, `map`, `node`, `edge`, `path`. `typeOf(null)` is `"null"` |

### Graph

| Function | Result |
|----------|--------|
| `id(entity)` | int id of a node or an edge |
| `labels(node)` | sorted label list |
| `type(edge)` | relationship type |
| `startNode(edge)`, `endNode(edge)` | stored endpoints |
| `properties(entity)` | property map of a node, edge, or map |
| `degree(node)`, `degree(node, dir)` | `dir` is `"out"`, `"in"`, or `"both"`. The one-argument form is `"out"` |
| `neighbors(node, depth)` | nodes reached by outgoing BFS, start excluded, each node once. `depth` is an int `>= 1`. Order is layer, then id |
| `shortestPath(a, b)`, `allShortestPaths(a, b)` | directed outgoing, any type, tie-break defined above |
| `length(path)` | hop count |
| `nodes(path)`, `relationships(path)` | walk order |

### Aggregates

| Function | Result |
|----------|--------|
| `count(*)` | number of rows, as int |
| `count(expr)`, `count(DISTINCT expr)` | non-null inputs. Empty input is `0` |
| `sum(expr)` | int if every input is int and the total fits, otherwise float when any input is float. Empty input is null. Overflow is `InvalidArgument` |
| `avg(expr)` | float. Empty input is null |
| `min(expr)`, `max(expr)` | `value.Compare`, skipping null. Empty input is null |
| `collect(expr)`, `collect(DISTINCT expr)` | list, skipping null. Empty input is `[]` |
| `percentileDisc(expr, p)` | discrete percentile. `p` is a float from 0 to 1. Sort non-null inputs by `value.Compare` and take index `ceil(p * n) - 1`, clamped to the list. Empty input is null |
| `stdev(expr)` | sample standard deviation with divisor `n - 1`, as float. Fewer than two non-null inputs is null |

`DISTINCT` inside an aggregate uses `value.Equal`.

## Procedures

`CALL db.labels()` yields `label`. `CALL db.edgeTypes()` yields `edgeType`. `CALL db.propertyKeys()` yields `propertyKey`. Each is a string column, sorted by byte order, one row per name in the catalog.

`CALL` with `YIELD` renames those columns into scope. A `CALL` that is the whole statement returns the procedure columns. A `CALL` in front of another clause yields into the row.

## DDL

Index and constraint names are case-sensitive identifiers, unique among objects of that kind. `CREATE` of a name that exists is `AlreadyExists`. `DROP` of a name that does not exist is `NotFound`.

```
CREATE INDEX person_name FOR (n:Person) ON (n.name);
CREATE INDEX knows_since FOR ()-[r:KNOWS]-() ON (r.since);
CREATE CONSTRAINT person_email_unique FOR (n:Person) REQUIRE n.email IS UNIQUE;
CREATE CONSTRAINT person_name_exists FOR (n:Person) REQUIRE n.name IS NOT NULL;
CREATE CONSTRAINT person_age_type FOR (n:Person) REQUIRE n.age IS :: INT;
```

The parenthesized variable in `FOR` and the variable in `ON` or `REQUIRE` are the same name. A property list has at least one property. Enforcement, index population, and the states `ONLINE`, `POPULATING`, and `FAILED` are Phase 5. Until a build finishes, `SHOW INDEXES` and `SHOW CONSTRAINTS` still return the catalog row.

`SHOW INDEXES` columns: `name`, `entity` (`"node"` or `"edge"`), `labelOrType`, `properties` (list of strings), `state`. `SHOW CONSTRAINTS` adds `kind` (`"unique"`, `"exists"`, or `"type"`) and `typeName` (null except for a type constraint).

`SHOW LABELS`, `SHOW EDGE TYPES`, and `SHOW PROPERTY KEYS` return one column, `name`, sorted. `SHOW STATS` returns one row: `nodes`, `edges`, and `labels` (the sorted label list). `ANALYZE` refreshes planner statistics; that refresh is Phase 5. The statement is legal in this grammar.

## DCL

These statements are in the grammar. Authentication and authorization are Phase 7. Multi-database execution is Phase 6. Until those phases, executing them is `Unavailable`, except `SHOW` forms that only read names the catalog already has.

A database name matches `[a-z][a-z0-9_]{0,62}`. `system` and `default` are reserved, compared case-insensitively. `DROP DATABASE name` without `CONFIRM` is `Semantic`. User names, role names, and other database names are stored as written.

`CREATE TOKEN` returns one row with columns `id` (int) and `token` (string). The token string appears in that result and is not written to the log. `REVOKE TOKEN id` drops it. `EXPIRES IN` takes a duration expression.

Privileges are `READ`, `WRITE`, and `ADMIN` on one database. `DENY` beats `GRANT` when both exist. Role membership is `GRANT ROLE name TO user`.

## Administration

`VACUUM` removes versions no open snapshot can read, as in [spec/transactions.md](transactions.md), and returns one column `reclaimed`. `SHOW TRANSACTIONS` returns `id`, `readOnly`, and `age` (a duration). `TERMINATE TRANSACTION id` rolls that transaction back. An unknown id is `NotFound`.

`HELP`, `EXIT`, and `QUIT` are shell commands. They are not part of this grammar. `SAVE` and `LOAD` are legacy statements. The rewrite fails them with `InvalidArgument` and the message `use graphdb backup`.

## Errors

Syntax and semantic errors include `at L:C` in the message, pointing at the start of the offending token.

| Condition | Code |
|-----------|------|
| Bad token, bad escape, unclosed string or comment, broken grammar, `BEGIN READ` | `Syntax` |
| Unbound or double-bound name, illegal clause order, aggregate placed illegally, `MERGE` shape, `UNION` column mismatch, reserved word used as a variable, `DROP DATABASE` without `CONFIRM` | `Semantic` |
| Wrong runtime kind, division by zero, overflow, unknown function, missing parameter, unstorable value, `SKIP`/`LIMIT` domain | `InvalidArgument` |
| Unique, existence, or type constraint; `DELETE` of a node that still has edges | `ConstraintViolation` |
| Write-write conflict at commit | `Conflict` |
| Hop limit, query memory budget, query timeout | `ResourceExhausted` |
| Unknown `DROP` target, unknown transaction id | `NotFound` |
| `CREATE` of an index, constraint, database, user, or role that exists | `AlreadyExists` |
| DCL executed before Phase 6 or Phase 7 wires it up | `Unavailable` |
| Authentication and authorization failures | `Unauthenticated`, `PermissionDenied` |
| A panic recovered by the server | `Internal` |

`Conflict`, `Unavailable`, and `ResourceExhausted` are retryable. `ConstraintViolation` is not.

## Legacy rewrite

`internal/compat/legacy` parses the line language and produces this AST. A bare word in a legacy property map becomes a string. The rewrite attaches a `DeprecatedSyntax` notification.

| Legacy | GQL-lite |
|--------|----------|
| `CREATE NODE person {name: "Alice", age: 30}` | `CREATE (:person {name: "Alice", age: 30})` |
| `CREATE NODE city {name: Paris}` | `CREATE (:city {name: "Paris"})` |
| `CREATE EDGE 1 -KNOWS-> 2 {since: 2020}` | `MATCH (a), (b) WHERE id(a) = 1 AND id(b) = 2 CREATE (a)-[:KNOWS {since: 2020}]->(b)` |
| `MATCH person WHERE age > 25` | `MATCH (n:person) WHERE n.age > 25 RETURN n` |
| `MATCH EDGE KNOWS WHERE since >= 2020` | `MATCH ()-[e:KNOWS]->() WHERE e.since >= 2020 RETURN e` |
| `GET NODE 1` | `MATCH (n) WHERE id(n) = 1 RETURN n` |
| `GET EDGE 1` | `MATCH ()-[e]->() WHERE id(e) = 1 RETURN e` |
| `EDGES 1 TO 2` | `MATCH (a)-[e]->(b) WHERE id(a) = 1 AND id(b) = 2 RETURN e` |
| `NEIGHBORS 1 DEPTH 2` | `MATCH (n) WHERE id(n) = 1 UNWIND neighbors(n, 2) AS m RETURN m` |
| `PATH 1 TO 2` | `MATCH (a), (b) WHERE id(a) = 1 AND id(b) = 2 RETURN shortestPath(a, b)` |
| `UPDATE NODE 1 {age: 31}` | `MATCH (n) WHERE id(n) = 1 SET n += {age: 31}` |
| `UPDATE EDGE 1 {since: 2021}` | `MATCH ()-[e]->() WHERE id(e) = 1 SET e += {since: 2021}` |
| `DELETE NODE 1` | `MATCH (n) WHERE id(n) = 1 DETACH DELETE n` |
| `DELETE EDGE 1` | `MATCH ()-[e]->() WHERE id(e) = 1 DELETE e` |
| `SHOW STATS` | `SHOW STATS` |
| `BEGIN` / `BEGIN READ ONLY` / `COMMIT` / `ROLLBACK` / `VACUUM` | the same statement |

`NEIGHBORS` without `DEPTH` uses depth 1. Legacy `WHERE` comparisons keep their meaning because a missing property is null and `WHERE` keeps only true.

## Not in this version

Named time zones, `EXISTS` subqueries, `FOREACH`, dynamic labels, zero-hop patterns, parameterized list types, positional parameters, and edge-endpoint label constraints are outside grammar version 1.

## Examples

Each block is one statement or one script the parser accepts, unless the line says it is rejected.

### E1. Match a labeled node

```gql
MATCH (a:Person {name: "Alice"})
RETURN a.name AS name;
```

One column `name`. A node with no `name` yields null and still matches, because the map predicate is absent.

### E2. Filter, project, sort, page

```gql
MATCH (a:Person)-[k:KNOWS*1..3]->(b:Person)
WHERE b.age >= 18 AND NOT b.name STARTS WITH "Z"
  AND ALL (e IN k WHERE e.since IS NOT NULL)
RETURN b.name AS name, size(k) AS hops, collect(DISTINCT b.city) AS cities
ORDER BY hops DESC, name
SKIP 10 LIMIT 20;
```

`k` is the list of edges, so the hop count is `size(k)`. `name` and `hops` are the grouping keys for `collect`. `k.since` on that list is `Semantic`.

### E3. Shortest path pattern

```gql
MATCH (a), (b)
WHERE id(a) = $from AND id(b) = $to
MATCH p = shortestPath((a)-[*]->(b))
RETURN p;
```

### E4. Neighbors

```gql
MATCH (a) WHERE id(a) = 1
RETURN neighbors(a, 2);
```

### E5. Optional match

```gql
MATCH (a:Person)
OPTIONAL MATCH (a)-[:LIVES_IN]->(c:City)
RETURN a.name AS name, c.name AS city;
```

`city` is null when no city matches.

### E6. Unwind a parameter

```gql
UNWIND $list AS x
RETURN x ORDER BY x;
```

### E7. With and where

```gql
MATCH (a:Person)
WITH a.name AS name, count(*) AS n
WHERE n > 1
RETURN name, n;
```

`count(*)` groups by `name`.

### E8. Union

```gql
MATCH (a:Person) RETURN a.name AS name
UNION
MATCH (c:City) RETURN c.name AS name;
```

Duplicate names disappear. `UNION ALL` keeps them.

### E9. Create a path

```gql
CREATE (a:Person {name: "Alice", tags: ["x", "y"]})-[:KNOWS {since: 2020}]->(b:Person {name: "Bob"})
RETURN id(a), id(b);
```

### E10. Merge

```gql
MERGE (a:Person {email: $email})
  ON CREATE SET a.created = datetime("2026-10-07T00:00:00Z")
  ON MATCH SET a.seen = a.seen + 1
RETURN id(a);
```

On the create step, `seen` is unset. On a later match, `a.seen + 1` is null because the missing property reads as null, and assigning null removes the property. A counter is `ON MATCH SET a.seen = coalesce(a.seen, 0) + 1`.

### E11. Set and remove

```gql
MATCH (a:Person)
WHERE a.age < 0
SET a.age = 0, a:Flagged
REMOVE a.temp
RETURN a;
```

### E12. Detach delete

```gql
MATCH (a:Person {name: "Bob"})
DETACH DELETE a;
```

### E13. Delete that still has edges

```gql
MATCH (a:Person {name: "Bob"})
DELETE a;
```

The parser accepts this. Execution is `ConstraintViolation` while any edge touches `a`.

### E14. Transaction script

```gql
BEGIN;
MATCH (a:Person) WHERE a.name = "Alice" SET a.age = 31;
COMMIT;
```

### E15. Read-only transaction

```gql
BEGIN READ ONLY;
MATCH (a:Person) RETURN a.name;
COMMIT;
```

### E16. Index and constraints

```gql
CREATE INDEX person_name FOR (n:Person) ON (n.name);
CREATE INDEX knows_since FOR ()-[r:KNOWS]-() ON (r.since);
CREATE CONSTRAINT person_email_unique FOR (n:Person) REQUIRE n.email IS UNIQUE;
CREATE CONSTRAINT person_name_exists FOR (n:Person) REQUIRE n.name IS NOT NULL;
CREATE CONSTRAINT person_age_type FOR (n:Person) REQUIRE n.age IS :: INT;
DROP INDEX person_name;
DROP CONSTRAINT person_email_unique;
```

### E17. Show

```gql
SHOW INDEXES;
SHOW CONSTRAINTS;
SHOW LABELS;
SHOW EDGE TYPES;
SHOW PROPERTY KEYS;
SHOW STATS;
```

### E18. Databases, users, grants

```gql
CREATE DATABASE analytics;
SHOW DATABASES;
USE analytics;
CREATE USER alice SET PASSWORD "s3cret" CHANGE REQUIRED;
ALTER USER alice SET PASSWORD "better";
CREATE ROLE analyst;
GRANT READ ON DATABASE analytics TO analyst;
GRANT ROLE analyst TO alice;
DENY WRITE ON DATABASE default TO analyst;
SHOW USERS;
SHOW ROLES;
SHOW PRIVILEGES FOR alice;
REVOKE ROLE analyst FROM alice;
DROP USER alice;
DROP DATABASE analytics CONFIRM;
```

### E19. Token

```gql
CREATE TOKEN FOR USER svc_app EXPIRES IN duration("P90D");
REVOKE TOKEN 1;
```

### E20. Explain, profile, vacuum, transactions

```gql
EXPLAIN MATCH (n:Person) RETURN n.name;
PROFILE MATCH (n:Person) RETURN n.name;
VACUUM;
SHOW TRANSACTIONS;
TERMINATE TRANSACTION 7;
ANALYZE;
```

### E21. Backtick identifier and keyword property

```gql
MATCH (n:Person)
RETURN n.`match` AS `return`;
```

### E22. Comments

```gql
-- line comment
MATCH (n:Person) /* block */ RETURN n.name; /* trailing */
```

### E23. Case and hex

```gql
MATCH (n) WHERE n.code = 0x2A OR n.score = 1.5e1 RETURN n;
```

`match` and `MATCH` are the same keyword. `0x2A` is the int 42. `1.5e1` is the float 15.

### E24. List comprehension and quantifiers

```gql
WITH [1, 2, 3, null] AS xs
RETURN [x IN xs WHERE x > 1 | x * 10] AS tens,
       ANY (x IN xs WHERE x = 2) AS hasTwo,
       ALL (x IN xs WHERE x IS NULL OR x > 0) AS ok;
```

`tens` is `[20, 30]`.

### E25. Reduce

```gql
RETURN REDUCE (s = 0, x IN [1, 2, 3] | s + x) AS sum;
```

The result is 6.

### E26. Case

```gql
MATCH (n:Person)
RETURN CASE WHEN n.age IS NULL THEN "missing" WHEN n.age >= 18 THEN "adult" ELSE "minor" END AS band;
```

Simple `CASE n.age WHEN 0 THEN "zero" ELSE "other" END` compares with predicate `=`.

### E27. IN, string predicates, type test

```gql
MATCH (n:Person)
WHERE n.name IN ["Ada", "Bob"]
  AND n.email STARTS WITH "a"
  AND n.age IS :: INT
RETURN n;
```

### E28. Slice and index

```gql
RETURN [1, 2, 3, 4][1..3] AS mid, [1, 2, 3][-1] AS last;
```

`mid` is `[2, 3]`. `last` is `3`.

### E29. Coalesce and typeof

```gql
RETURN coalesce(null, 1, 2) AS first, typeOf(1.0) AS k;
```

`first` is `1`. `k` is `"float"`.

### E30. String and list functions

```gql
RETURN toUpper("Ada") AS u, split("a,b", ",") AS parts,
       substring("hello", 1, 3) AS sub, size("héllo") AS chars,
       tail(reverse(range(1, 3))) AS xs;
```

`chars` is 5. `xs` is `[2, 1]`.

### E31. Math

```gql
RETURN abs(-3) AS a, round(1.5) AS r, sign(-0.0) AS s, sqrt(4) AS q;
```

`r` is `2.0`. `s` is `0` because `-0.0` equals `0`.

### E32. Temporal arithmetic

```gql
RETURN date("2025-01-31") + duration("P1M") AS d,
       datetime("2025-01-31T00:00:00+05:30") + duration("P1D") AS t,
       year(date("2024-02-29")) AS y;
```

`d` is `2025-02-28`. `t` keeps offset `+05:30` and lands on February 1. `y` is `2024`.

### E33. Path functions

```gql
MATCH (a), (b)
WHERE id(a) = 1 AND id(b) = 2
WITH shortestPath(a, b) AS p
RETURN length(p) AS hops, nodes(p) AS ns, relationships(p) AS es;
```

No path makes `p` null, and `length(null)` is null.

### E34. Degree and properties

```gql
MATCH (n) WHERE id(n) = 1
RETURN degree(n, "both") AS d, keys(n) AS ks, properties(n) AS props;
```

### E35. Distinct count

```gql
MATCH (a:Person)-[:KNOWS]->(b)
RETURN count(DISTINCT b.city) AS cities;
```

### E36. Grouping

```gql
MATCH (a:Person)
RETURN labels(a) AS labs, avg(a.age) AS mean, min(a.age) AS youngest
ORDER BY mean DESC NULLS LAST;
```

### E37. Set replace and merge maps

```gql
MATCH (n) WHERE id(n) = 1
SET n = {name: "Ada"}, n += {city: "Paris"}
RETURN n.name, n.city;
```

The replace drops every previous property. The merge then adds `city`.

### E38. Null removes a property

```gql
MATCH (n) WHERE id(n) = 1
SET n.temp = null
RETURN n.temp IS NULL AS gone;
```

`gone` is true.

### E39. Unwind then match

```gql
UNWIND [1, 2] AS idv
MATCH (n) WHERE id(n) = idv
RETURN n.name;
```

### E40. Comma pattern

```gql
MATCH (a:Person), (b:City)
WHERE a.city = b.name
RETURN a.name, b.name;
```

### E41. Incoming and undirected

```gql
MATCH (a)<-[:KNOWS]-(b) RETURN id(a), id(b);
MATCH (a)-[:KNOWS]-(b) RETURN id(b);
```

### E42. Several labels

```gql
MATCH (n:Person:Employee|Contractor) RETURN n;
```

`n` has label `Person` and also `Employee` or `Contractor`.

### E43. Call

```gql
CALL db.labels() YIELD label AS name
WHERE name STARTS WITH "P"
RETURN name ORDER BY name;
```

### E44. Create then read in one statement

```gql
CREATE (a:Person {name: "Cara"})
WITH a
MATCH (b:Person) WHERE id(b) = id(a)
RETURN b.name;
```

The second clause sees `a`.

### E45. Skip and limit parameters

```gql
MATCH (n:Person)
RETURN n.name ORDER BY n.name SKIP $offset LIMIT $limit;
```

### E46. Percentile and stdev

```gql
MATCH (n:Person)
RETURN percentileDisc(n.age, 0.5) AS median, stdev(n.age) AS spread;
```

### E47. Bytes and maps

```gql
RETURN bytes("aGVsbG8=") AS raw, keys({b: 1, a: 2}) AS ks;
```

`ks` is `["a", "b"]`.

### E48. XOR

```gql
RETURN true XOR false AS t, null XOR true AS u;
```

`t` is true. `u` is null.

### E49. Short-circuit

```gql
RETURN false AND (1 / 0 = 1) AS safe;
```

`safe` is false. The division does not run.

### E50. Read-only rejection

```gql
BEGIN READ ONLY;
CREATE (:Person {name: "nope"});
```

The second statement is `InvalidArgument`. The transaction stays open.

### E51. Illegal merge

```gql
MERGE (a)-[:KNOWS*1..2]->(b) RETURN a;
```

`Semantic`, because `MERGE` has no variable-length relationship.

### E52. Aggregate in where

```gql
MATCH (n:Person) WHERE count(n) > 1 RETURN n;
```

`Semantic`.

### E53. Begin read

```gql
BEGIN READ;
```

`Syntax`.

### E54. Missing parameter

```gql
MATCH (n) WHERE n.name = $name RETURN n;
```

`InvalidArgument` when `name` was not supplied.

### E55. Database confirm

```gql
DROP DATABASE analytics;
```

`Semantic`. `DROP DATABASE analytics CONFIRM` is the legal form.

### E56. Self-loop and isomorphism

```gql
MATCH (a)-[e:KNOWS]->(b)-[f:KNOWS]->(a)
WHERE id(e) <> id(f)
RETURN e, f;
```

The two edges are different stored edges.

### E57. All shortest paths

```gql
MATCH (a), (b)
WHERE id(a) = $from AND id(b) = $to
RETURN allShortestPaths(a, b) AS paths;
```

### E58. Exists and type constraints in one script

```gql
CREATE CONSTRAINT city_name_exists FOR (n:City) REQUIRE n.name IS NOT NULL;
CREATE CONSTRAINT city_pop_type FOR (n:City) REQUIRE n.pop IS :: INT;
MATCH (n:City) WHERE n.name IS NOT NULL RETURN n.name;
```

### E59. Multi-hop function form is rejected as a property

```gql
MATCH p = (a:Person)-[:KNOWS*]->(b:Person)
RETURN relationships(p) AS edges, length(p) AS hops;
```

### E60. Script of two auto-commits

```gql
CREATE (:Person {name: "Diya"});
MATCH (n:Person {name: "Diya"}) RETURN id(n);
```

Each statement commits on its own when no `BEGIN` is open.
