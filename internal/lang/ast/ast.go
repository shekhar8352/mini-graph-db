// Package ast is the typed GQL-lite tree. Grammar version 1.
// Parse errors carry positions; nodes do not, so two parses compare equal
// after formatting.
package ast

// Node is any tree element.
type Node interface{ node() }

// Stmt is a script statement.
type Stmt interface {
	Node
	stmtNode()
}

// Expr is an expression.
type Expr interface {
	Node
	exprNode()
}

// Clause is one clause of a query.
type Clause interface {
	Node
	clauseNode()
}

type expr struct{}

func (*expr) node()     {}
func (*expr) exprNode() {}

type stmt struct{}

func (*stmt) node()     {}
func (*stmt) stmtNode() {}

type clause struct{}

func (*clause) node()       {}
func (*clause) clauseNode() {}

type tree struct{}

func (*tree) node() {}

// Script is a sequence of statements. Empty input is an empty script.
type Script struct {
	tree
	Stmts []Stmt
}

// Query is one or more single queries joined by UNION.
// All[i] is true when the union before Parts[i+1] is UNION ALL.
// A bare UNION and UNION DISTINCT are both All false.
type Query struct {
	stmt
	Parts []*SingleQuery
	All   []bool
}

// SingleQuery is a clause list.
type SingleQuery struct {
	tree
	Clauses []Clause
}

// Begin starts a transaction.
type Begin struct {
	stmt
	ReadOnly bool
}

// Commit commits the current transaction.
type Commit struct{ stmt }

// Rollback aborts the current transaction.
type Rollback struct{ stmt }

// Explain is EXPLAIN query.
type Explain struct {
	stmt
	Query *Query
}

// Profile is PROFILE query.
type Profile struct {
	stmt
	Query *Query
}

// Vacuum is VACUUM.
type Vacuum struct{ stmt }

// Analyze is ANALYZE.
type Analyze struct{ stmt }

// Use is USE name.
type Use struct {
	stmt
	Name string
}

// Terminate is TERMINATE TRANSACTION id.
type Terminate struct {
	stmt
	ID int64
}

// ShowKind is the body of SHOW.
type ShowKind string

// SHOW bodies.
const (
	ShowIndexes      ShowKind = "INDEXES"
	ShowConstraints  ShowKind = "CONSTRAINTS"
	ShowLabels       ShowKind = "LABELS"
	ShowEdgeTypes    ShowKind = "EDGE TYPES"
	ShowPropertyKeys ShowKind = "PROPERTY KEYS"
	ShowStats        ShowKind = "STATS"
	ShowDatabases    ShowKind = "DATABASES"
	ShowUsers        ShowKind = "USERS"
	ShowRoles        ShowKind = "ROLES"
	ShowPrivileges   ShowKind = "PRIVILEGES"
	ShowTransactions ShowKind = "TRANSACTIONS"
)

// Show is a SHOW statement. Name is set for SHOW PRIVILEGES FOR name.
type Show struct {
	stmt
	Kind ShowKind
	Name string
}

// Dir is a relationship direction.
type Dir int

// Relationship directions.
const (
	DirOut Dir = iota
	DirIn
	DirEither
)

// PathFn selects the plain or shortest-path arm.
type PathFn int

// Path arm kinds.
const (
	PathPlain PathFn = iota
	PathShortest
	PathAllShortest
)

// Pattern is a comma-separated list of paths.
type Pattern struct {
	tree
	Paths []*Path
}

// Path is one chain. Nodes has one more element than Rels.
type Path struct {
	tree
	Var   string
	Fn    PathFn
	Nodes []*NodePat
	Rels  []*RelPat
}

// NodePat is a parenthesized node.
type NodePat struct {
	tree
	Name   string
	Labels [][]string
	Props  *MapLit
}

// RelPat is one relationship. VarLen false means exactly one hop.
// Max < 0 means the upper bound is open. Min 1 and Max < 0 is `*`.
type RelPat struct {
	tree
	Dir    Dir
	Name   string
	Types  []string
	VarLen bool
	Min    int64
	Max    int64
	Props  *MapLit
}

// Match is MATCH or OPTIONAL MATCH.
type Match struct {
	clause
	Optional bool
	Pattern  *Pattern
	Where    Expr
}

// Unwind is UNWIND expr AS name.
type Unwind struct {
	clause
	Expr Expr
	Name string
}

// With is a WITH clause.
type With struct {
	clause
	Body  *ReturnBody
	Where Expr
}

// Return is a RETURN clause.
type Return struct {
	clause
	Body *ReturnBody
}

// ReturnBody is the projection shared by WITH and RETURN.
type ReturnBody struct {
	tree
	Distinct bool
	Star     bool
	Items    []*Projection
	Order    []*Sort
	Skip     Expr
	Limit    Expr
}

// Projection is one RETURN or WITH item.
type Projection struct {
	tree
	Expr Expr
	As   string
}

// Nulls is the NULLS FIRST / LAST marker. Zero means omitted.
type Nulls int

// NULLS placement.
const (
	NullsDefault Nulls = iota
	NullsFirst
	NullsLast
)

// Sort is one ORDER BY item. Desc false prints as ASC, which is omitted.
type Sort struct {
	tree
	Expr  Expr
	Desc  bool
	Nulls Nulls
}

// Call is CALL proc(args) YIELD ... WHERE filters the yielded rows.
type Call struct {
	clause
	Proc  []string
	Args  []Expr
	Yield []*Yield
	Where Expr
}

// Yield is one YIELD name.
type Yield struct {
	tree
	Name string
	As   string
}

// Create is a CREATE clause.
type Create struct {
	clause
	Pattern *Pattern
}

// Merge is a MERGE clause. OnCreate and OnMatch are the SET clauses.
type Merge struct {
	clause
	Path     *Path
	OnCreate *Set
	OnMatch  *Set
}

// SetOp is one SET item's form.
type SetOp int

// SET forms.
const (
	SetProp SetOp = iota
	SetReplace
	SetPlus
	SetLabels
)

// Set is a SET clause.
type Set struct {
	clause
	Items []*SetItem
}

// SetItem is one assignment or label update.
type SetItem struct {
	tree
	Op     SetOp
	Name   string
	Prop   string
	Value  Expr
	Labels [][]string
}

// Remove is a REMOVE clause.
type Remove struct {
	clause
	Items []*RemoveItem
}

// RemoveItem drops a property or labels.
type RemoveItem struct {
	tree
	Name   string
	Prop   string
	Labels [][]string
}

// Delete is DELETE or DETACH DELETE.
type Delete struct {
	clause
	Detach bool
	Exprs  []Expr
}

// LitKind is the kind of a literal.
type LitKind int

// Literal kinds.
const (
	LitNull LitKind = iota
	LitBool
	LitInt
	LitFloat
	LitString
	LitNaN
	LitInf
)

// Literal is a literal value. Inf is the INF keyword. A float overflow is LitFloat.
type Literal struct {
	expr
	Kind  LitKind
	Bool  bool
	Int   int64
	Float float64
	Str   string
}

// Ident is a variable or other name used as an expression.
type Ident struct {
	expr
	Name string
}

// Param is $name.
type Param struct {
	expr
	Name string
}

// Unary is NOT, unary plus, or unary minus.
type Unary struct {
	expr
	Op string
	X  Expr
}

// Binary is a binary operator, including IN and STARTS WITH.
// != is stored as <>.
type Binary struct {
	expr
	Op    string
	Left  Expr
	Right Expr
}

// Pred is IS [NOT] NULL or IS [NOT] :: type.
type Pred struct {
	expr
	X    Expr
	Not  bool
	Type string
}

// Property is expr.name.
type Property struct {
	expr
	X    Expr
	Name string
}

// Index is expr[index].
type Index struct {
	expr
	X     Expr
	Index Expr
}

// Slice is expr[low..high]. HasHigh is false when the upper bound is omitted.
type Slice struct {
	expr
	X       Expr
	Low     Expr
	High    Expr
	HasHigh bool
}

// List is a list literal.
type List struct {
	expr
	Elems []Expr
}

// MapLit is a map literal or a pattern property map.
type MapLit struct {
	expr
	Entries []*MapEntry
}

// MapEntry is one map key. Quoted is true when the key was a string.
type MapEntry struct {
	tree
	Key    string
	Quoted bool
	Value  Expr
}

// Comp is a list comprehension. HasProj is false when the bar is omitted.
type Comp struct {
	expr
	Var     string
	In      Expr
	Where   Expr
	Proj    Expr
	HasProj bool
}

// Quant is ANY, ALL, NONE, or SINGLE.
type Quant struct {
	expr
	Kind  string
	Var   string
	In    Expr
	Where Expr
}

// Reduce is REDUCE.
type Reduce struct {
	expr
	Acc  string
	Init Expr
	Var  string
	In   Expr
	Body Expr
}

// Case is a simple or searched case. Input is set for the simple form.
type Case struct {
	expr
	Input Expr
	Whens []*When
	Else  Expr
}

// When is one CASE arm.
type When struct {
	tree
	Cond Expr
	Then Expr
}

// CallExpr is a function call. Star is count(*).
type CallExpr struct {
	expr
	Name     string
	Distinct bool
	Star     bool
	Args     []Expr
}

// IndexFor is the FOR target of an index or constraint.
// Edge is true for ()-[var:type]-().
type IndexFor struct {
	tree
	Edge bool
	Var  string
	On   string
}

// CreateIndex is CREATE INDEX.
type CreateIndex struct {
	stmt
	Name  string
	For   *IndexFor
	Props []*PropRef
}

// PropRef is var.prop in DDL.
type PropRef struct {
	tree
	Var  string
	Prop string
}

// DropIndex is DROP INDEX.
type DropIndex struct {
	stmt
	Name string
}

// ConstraintKind is UNIQUE, exists, or a type check.
type ConstraintKind int

// Constraint kinds.
const (
	ConstraintUnique ConstraintKind = iota
	ConstraintExists
	ConstraintType
)

// CreateConstraint is CREATE CONSTRAINT.
type CreateConstraint struct {
	stmt
	Name string
	For  *IndexFor
	Var  string
	Prop string
	Kind ConstraintKind
	Type string
}

// DropConstraint is DROP CONSTRAINT.
type DropConstraint struct {
	stmt
	Name string
}

// CreateDatabase is CREATE DATABASE.
type CreateDatabase struct {
	stmt
	Name string
}

// DropDatabase is DROP DATABASE. Confirm is the CONFIRM keyword.
type DropDatabase struct {
	stmt
	Name    string
	Confirm bool
}

// CreateUser is CREATE USER.
type CreateUser struct {
	stmt
	Name           string
	Password       string
	ChangeRequired bool
}

// AlterUser is ALTER USER.
type AlterUser struct {
	stmt
	Name           string
	Password       string
	ChangeRequired bool
}

// DropUser is DROP USER.
type DropUser struct {
	stmt
	Name string
}

// CreateRole is CREATE ROLE.
type CreateRole struct {
	stmt
	Name string
}

// DropRole is DROP ROLE.
type DropRole struct {
	stmt
	Name string
}

// GrantRole is GRANT ROLE.
type GrantRole struct {
	stmt
	Role string
	To   string
}

// RevokeRole is REVOKE ROLE.
type RevokeRole struct {
	stmt
	Role string
	From string
}

// Priv is READ, WRITE, or ADMIN.
type Priv string

// Privileges.
const (
	PrivRead  Priv = "READ"
	PrivWrite Priv = "WRITE"
	PrivAdmin Priv = "ADMIN"
)

// GrantPriv is GRANT priv ON DATABASE.
type GrantPriv struct {
	stmt
	Priv     Priv
	Database string
	To       string
}

// RevokePriv is REVOKE priv ON DATABASE.
type RevokePriv struct {
	stmt
	Priv     Priv
	Database string
	From     string
}

// DenyPriv is DENY priv ON DATABASE.
type DenyPriv struct {
	stmt
	Priv     Priv
	Database string
	To       string
}

// CreateToken is CREATE TOKEN.
type CreateToken struct {
	stmt
	User    string
	Expires Expr
}

// RevokeToken is REVOKE TOKEN.
type RevokeToken struct {
	stmt
	ID int64
}
