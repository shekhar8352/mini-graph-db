// Package plan builds a logical plan for a GQL-lite script and prints it.
// The printed text is the EXPLAIN format: one operator per line, root first,
// children indented by two spaces, input first. It does not run the query.
package plan

import (
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
)

// Op is one logical operator. A nil input is a single empty driving row.
type Op interface{ op() }

// Plan is one script, in statement order.
type Plan struct {
	Stmts []Op
}

// String prints every statement. Trees are separated by a blank line.
func (p *Plan) String() string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	for i, st := range p.Stmts {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(Format(st))
	}
	return b.String()
}

// Format prints one operator tree. The text ends with a newline.
func Format(op Op) string {
	if op == nil {
		return ""
	}
	var b strings.Builder
	write(&b, op, 0)
	return b.String()
}

func write(b *strings.Builder, op Op, indent int) {
	for i := 0; i < indent; i++ {
		b.WriteString("  ")
	}
	b.WriteString(title(op))
	b.WriteByte('\n')
	for _, kid := range children(op) {
		write(b, kid, indent+1)
	}
}

// NodeScan reads every node. Node is the pattern, including an anonymous node.
type NodeScan struct {
	Node *ast.NodePat
	Path string
}

func (*NodeScan) op() {}

// LabelScan reads nodes that carry the pattern's required labels.
type LabelScan struct {
	Node *ast.NodePat
	Path string
}

func (*LabelScan) op() {}

// IndexSeek is an equality lookup on one label and the pattern's property map.
// A node pattern with a single label and at least one property uses this operator.
type IndexSeek struct {
	Node *ast.NodePat
	Path string
}

func (*IndexSeek) op() {}

// Expand traverses one relationship from a bound start node.
// Hop's first node is that start. Path names the whole chain when this is its last hop.
type Expand struct {
	Input Op
	Hop   *ast.Path
	Path  string
}

func (*Expand) op() {}

// Filter keeps rows for which Expr is true, or for which the bound node matches Node.
type Filter struct {
	Input Op
	Expr  ast.Expr
	Node  *ast.NodePat
}

func (*Filter) op() {}

// Project evaluates expressions and replaces the row.
type Project struct {
	Input Op
	Items []*ast.Projection
	Star  []string
}

func (*Project) op() {}

// Aggregate groups by the non-aggregate items and evaluates the aggregate items.
type Aggregate struct {
	Input Op
	Items []*ast.Projection
}

func (*Aggregate) op() {}

// Sort orders rows. Keys are in ORDER BY order.
type Sort struct {
	Input Op
	Keys  []*ast.Sort
}

func (*Sort) op() {}

// Limit keeps the first Count rows.
type Limit struct {
	Input Op
	Count ast.Expr
}

func (*Limit) op() {}

// Skip drops the first Count rows.
type Skip struct {
	Input Op
	Count ast.Expr
}

func (*Skip) op() {}

// Distinct drops duplicate rows.
type Distinct struct{ Input Op }

func (*Distinct) op() {}

// Union combines two query plans. All is true for UNION ALL.
type Union struct {
	Left  Op
	Right Op
	All   bool
}

func (*Union) op() {}

// Unwind turns a list into one row per element.
type Unwind struct {
	Input Op
	Expr  ast.Expr
	Name  string
}

func (*Unwind) op() {}

// Optional keeps the input row and supplies nulls when its input produces nothing.
// Names are the variables this optional pattern bound; they become null on a miss.
type Optional struct {
	Input Op
	Names []string
}

func (*Optional) op() {}

// Apply runs Right once for each row of Left.
// NewMatch starts a new MATCH: the right side sees the left bindings and a fresh edge set.
type Apply struct {
	Left     Op
	Right    Op
	NewMatch bool
}

func (*Apply) op() {}

// Create inserts the pattern for each input row.
type Create struct {
	Input   Op
	Pattern *ast.Pattern
}

func (*Create) op() {}

// Merge matches or creates one path. OnCreate and OnMatch are the SET actions.
type Merge struct {
	Input    Op
	Path     *ast.Path
	OnCreate *ast.Set
	OnMatch  *ast.Set
}

func (*Merge) op() {}

// SetProps applies property assignments.
type SetProps struct {
	Input Op
	Items []*ast.SetItem
}

func (*SetProps) op() {}

// SetLabels adds labels.
type SetLabels struct {
	Input Op
	Items []*ast.SetItem
}

func (*SetLabels) op() {}

// Remove drops properties or labels.
type Remove struct {
	Input Op
	Items []*ast.RemoveItem
}

func (*Remove) op() {}

// Delete deletes nodes or edges. Detach deletes incident edges first.
type Delete struct {
	Input  Op
	Exprs  []ast.Expr
	Detach bool
}

func (*Delete) op() {}

// ShortestPath solves one shortest-path pattern.
type ShortestPath struct {
	Input Op
	Path  *ast.Path
}

func (*ShortestPath) op() {}

// Leaf is a statement that has no row pipeline: DDL, DCL, or administration.
type Leaf struct {
	Name   string
	Detail string
}

func (*Leaf) op() {}

func title(op Op) string {
	switch n := op.(type) {
	case *NodeScan:
		return "NodeScan(" + nodeArg(n.Node, n.Path) + ")"
	case *LabelScan:
		return "LabelScan(" + nodeArg(n.Node, n.Path) + ")"
	case *IndexSeek:
		return "IndexSeek(" + nodeArg(n.Node, n.Path) + ")"
	case *Expand:
		return "Expand(" + hopArg(n) + ")"
	case *Filter:
		if n.Expr != nil {
			return "Filter(" + ast.Format(n.Expr) + ")"
		}
		return "Filter(" + nodeArg(n.Node, "") + ")"
	case *Project:
		return "Project(" + projArg(n.Items, n.Star) + ")"
	case *Aggregate:
		return "Aggregate(" + projArg(n.Items, nil) + ")"
	case *Sort:
		return "Sort(" + sortArg(n.Keys) + ")"
	case *Limit:
		return "Limit(" + ast.Format(n.Count) + ")"
	case *Skip:
		return "Skip(" + ast.Format(n.Count) + ")"
	case *Distinct:
		return "Distinct"
	case *Union:
		if n.All {
			return "Union(ALL)"
		}
		return "Union"
	case *Unwind:
		return "Unwind(" + ast.Format(n.Expr) + " AS " + ast.Format(&ast.Ident{Name: n.Name}) + ")"
	case *Optional:
		return "Optional"
	case *Apply:
		return "Apply"
	case *Create:
		return "Create(" + ast.Format(n.Pattern) + ")"
	case *Merge:
		return "Merge(" + mergeArg(n) + ")"
	case *SetProps:
		return "SetProps(" + setArg(n.Items) + ")"
	case *SetLabels:
		return "SetLabels(" + setArg(n.Items) + ")"
	case *Remove:
		return "Remove(" + removeArg(n.Items) + ")"
	case *Delete:
		return "Delete(" + deleteArg(n) + ")"
	case *ShortestPath:
		return "ShortestPath(" + ast.Format(n.Path) + ")"
	case *Leaf:
		if n.Detail == "" {
			return n.Name
		}
		return n.Name + "(" + n.Detail + ")"
	default:
		return "Unknown"
	}
}

func children(op Op) []Op {
	switch n := op.(type) {
	case *Expand:
		return one(n.Input)
	case *Filter:
		return one(n.Input)
	case *Project:
		return one(n.Input)
	case *Aggregate:
		return one(n.Input)
	case *Sort:
		return one(n.Input)
	case *Limit:
		return one(n.Input)
	case *Skip:
		return one(n.Input)
	case *Distinct:
		return one(n.Input)
	case *Union:
		return pair(n.Left, n.Right)
	case *Unwind:
		return one(n.Input)
	case *Optional:
		return one(n.Input)
	case *Apply:
		return pair(n.Left, n.Right)
	case *Create:
		return one(n.Input)
	case *Merge:
		return one(n.Input)
	case *SetProps:
		return one(n.Input)
	case *SetLabels:
		return one(n.Input)
	case *Remove:
		return one(n.Input)
	case *Delete:
		return one(n.Input)
	case *ShortestPath:
		return one(n.Input)
	default:
		return nil
	}
}

func one(op Op) []Op {
	if op == nil {
		return nil
	}
	return []Op{op}
}

func pair(a, b Op) []Op {
	var out []Op
	if a != nil {
		out = append(out, a)
	}
	if b != nil {
		out = append(out, b)
	}
	return out
}
