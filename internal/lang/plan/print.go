package plan

import (
	"strconv"
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
)

func nodeArg(n *ast.NodePat, path string) string {
	text := ast.Format(&ast.Path{Nodes: []*ast.NodePat{n}})
	if path == "" {
		return text
	}
	return text + ", path " + ast.Format(&ast.Ident{Name: path})
}

func hopArg(e *Expand) string {
	text := ast.Format(e.Hop)
	if e.Path == "" {
		return text
	}
	return text + ", path " + ast.Format(&ast.Ident{Name: e.Path})
}

func projArg(items []*ast.Projection, star []string) string {
	if len(star) > 0 {
		return strings.Join(star, ", ")
	}
	parts := make([]string, len(items))
	for i, it := range items {
		base := ast.Format(it.Expr)
		if it.As != "" {
			parts[i] = base + " AS " + ast.Format(&ast.Ident{Name: it.As})
			continue
		}
		parts[i] = base
	}
	return strings.Join(parts, ", ")
}

func sortArg(keys []*ast.Sort) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		text := ast.Format(k.Expr)
		if k.Desc {
			text += " DESC"
		} else {
			text += " ASC"
		}
		switch k.Nulls {
		case ast.NullsFirst:
			text += " NULLS FIRST"
		case ast.NullsLast:
			text += " NULLS LAST"
		}
		parts[i] = text
	}
	return strings.Join(parts, ", ")
}

func mergeArg(m *Merge) string {
	text := ast.Format(m.Path)
	if m.OnCreate != nil {
		text += "; on create " + setClause(m.OnCreate)
	}
	if m.OnMatch != nil {
		text += "; on match " + setClause(m.OnMatch)
	}
	return text
}

func setClause(s *ast.Set) string {
	if s == nil {
		return ""
	}
	var parts []string
	var props, labels []*ast.SetItem
	flush := func() {
		if len(props) > 0 {
			parts = append(parts, "SetProps("+setArg(props)+")")
			props = nil
		}
		if len(labels) > 0 {
			parts = append(parts, "SetLabels("+setArg(labels)+")")
			labels = nil
		}
	}
	var mode string
	for _, it := range s.Items {
		kind := "prop"
		if it.Op == ast.SetLabels {
			kind = "label"
		}
		if mode != "" && mode != kind {
			flush()
		}
		mode = kind
		if kind == "label" {
			labels = append(labels, it)
			continue
		}
		props = append(props, it)
	}
	flush()
	return strings.Join(parts, ", ")
}

func setArg(items []*ast.SetItem) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = setItem(it)
	}
	return strings.Join(parts, ", ")
}

func setItem(it *ast.SetItem) string {
	if it.Op == ast.SetLabels {
		return nodeInner(it.Name, it.Labels)
	}
	name := ast.Format(&ast.Ident{Name: it.Name})
	switch it.Op {
	case ast.SetReplace:
		return name + " = " + ast.Format(it.Value)
	case ast.SetPlus:
		return name + " += " + ast.Format(it.Value)
	default:
		prop := ast.Format(&ast.Property{X: &ast.Ident{Name: it.Name}, Name: it.Prop})
		return prop + " = " + ast.Format(it.Value)
	}
}

func removeArg(items []*ast.RemoveItem) string {
	parts := make([]string, len(items))
	for i, it := range items {
		if it.Prop != "" {
			parts[i] = ast.Format(&ast.Property{X: &ast.Ident{Name: it.Name}, Name: it.Prop})
			continue
		}
		parts[i] = nodeInner(it.Name, it.Labels)
	}
	return strings.Join(parts, ", ")
}

func deleteArg(d *Delete) string {
	parts := make([]string, len(d.Exprs))
	for i, e := range d.Exprs {
		parts[i] = ast.Format(e)
	}
	body := strings.Join(parts, ", ")
	if d.Detach {
		return "DETACH " + body
	}
	return body
}

func nodeInner(name string, labels [][]string) string {
	text := ast.Format(&ast.Path{Nodes: []*ast.NodePat{{Name: name, Labels: labels}}})
	text = strings.TrimPrefix(text, "(")
	text = strings.TrimSuffix(text, ")")
	return text
}

func callText(c *ast.Call) string {
	var b strings.Builder
	for i, part := range c.Proc {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(part)
	}
	b.WriteByte('(')
	for i, a := range c.Args {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(ast.Format(a))
	}
	b.WriteByte(')')
	return b.String()
}

func leafDetail(id int64) string { return strconv.FormatInt(id, 10) }
