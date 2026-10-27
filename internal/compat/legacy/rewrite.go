// Package legacy rewrites the line language into GQL-lite.
// The table is in docs/spec/query-language.md. A successful rewrite
// is a script the v2 parser accepts. HELP and EXIT stay shell commands.
// SAVE and LOAD fail with InvalidArgument.
package legacy

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/lang/lexer"
	"github.com/shekhar8352/mini-graph-db/internal/lang/parser"
	"github.com/shekhar8352/mini-graph-db/internal/query"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// Result is one compiled shell line.
type Result struct {
	Source  string
	Script  *ast.Script
	Help    bool
	Exit    bool
	Legacy  bool
	Message string
}

const deprecatedMsg = "legacy line syntax; rewritten to GQL-lite"

// Compile turns one shell line into GQL-lite.
// A line the v2 parser accepts is returned unchanged.
// A legacy statement is rewritten and marked Legacy.
func Compile(src string) (Result, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return Result{Source: src, Script: &ast.Script{}}, nil
	}
	if script, err := parser.Parse(src); err == nil {
		return Result{Source: src, Script: script}, nil
	}
	stmt, err := query.Parse(src)
	if err != nil {
		if script, v2err := parser.Parse(src); v2err == nil {
			return Result{Source: src, Script: script}, nil
		} else if looksLegacy(src) {
			return Result{}, err
		} else {
			return Result{}, v2err
		}
	}
	return rewriteStmt(stmt)
}

// Rewrite parses a legacy line and returns the GQL-lite script.
func Rewrite(src string) (Result, error) {
	stmt, err := query.Parse(src)
	if err != nil {
		return Result{}, err
	}
	return rewriteStmt(stmt)
}

func rewriteStmt(stmt query.Stmt) (Result, error) {
	switch stmt.(type) {
	case query.HelpStmt:
		return Result{Help: true}, nil
	case query.ExitStmt:
		return Result{Exit: true}, nil
	case query.SaveStmt, query.LoadStmt:
		return Result{}, gerr.New(gerr.InvalidArgument, "use `graphdb backup`")
	}
	src, err := rewriteSource(stmt)
	if err != nil {
		return Result{}, err
	}
	script, err := parser.Parse(src)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Source:  src,
		Script:  script,
		Legacy:  true,
		Message: deprecatedMsg,
	}, nil
}

func rewriteSource(stmt query.Stmt) (string, error) {
	switch s := stmt.(type) {
	case query.CreateNodeStmt:
		return "CREATE (:" + quoteIdent(s.Label) + formatProps(s.Props) + ")", nil
	case query.CreateEdgeStmt:
		return fmt.Sprintf(
			"MATCH (a), (b) WHERE id(a) = %d AND id(b) = %d CREATE (a)-[:%s%s]->(b)",
			s.From, s.To, quoteIdent(s.Label), formatProps(s.Props),
		), nil
	case query.MatchStmt:
		return "MATCH (n:" + quoteIdent(s.Label) + ")" + whereClause("n", s.Where) + " RETURN n", nil
	case query.MatchEdgeStmt:
		return "MATCH ()-[e:" + quoteIdent(s.Label) + "]->()" + whereClause("e", s.Where) + " RETURN e", nil
	case query.GetNodeStmt:
		return fmt.Sprintf("MATCH (n) WHERE id(n) = %d RETURN n", s.ID), nil
	case query.GetEdgeStmt:
		return fmt.Sprintf("MATCH ()-[e]->() WHERE id(e) = %d RETURN e", s.ID), nil
	case query.EdgesStmt:
		return fmt.Sprintf("MATCH (a)-[e]->(b) WHERE id(a) = %d AND id(b) = %d RETURN e", s.From, s.To), nil
	case query.NeighborsStmt:
		return fmt.Sprintf("MATCH (n) WHERE id(n) = %d UNWIND neighbors(n, %d) AS m RETURN m", s.ID, s.Depth), nil
	case query.PathStmt:
		return fmt.Sprintf("MATCH (a), (b) WHERE id(a) = %d AND id(b) = %d RETURN shortestPath(a, b)", s.From, s.To), nil
	case query.UpdateNodeStmt:
		return fmt.Sprintf("MATCH (n) WHERE id(n) = %d SET n += %s", s.ID, mapLit(s.Props)), nil
	case query.UpdateEdgeStmt:
		return fmt.Sprintf("MATCH ()-[e]->() WHERE id(e) = %d SET e += %s", s.ID, mapLit(s.Props)), nil
	case query.DeleteNodeStmt:
		return fmt.Sprintf("MATCH (n) WHERE id(n) = %d DETACH DELETE n", s.ID), nil
	case query.DeleteEdgeStmt:
		return fmt.Sprintf("MATCH ()-[e]->() WHERE id(e) = %d DELETE e", s.ID), nil
	case query.ShowStatsStmt:
		return "SHOW STATS", nil
	case query.BeginStmt:
		if s.ReadOnly {
			return "BEGIN READ ONLY", nil
		}
		return "BEGIN", nil
	case query.CommitStmt:
		return "COMMIT", nil
	case query.RollbackStmt:
		return "ROLLBACK", nil
	case query.VacuumStmt:
		return "VACUUM", nil
	default:
		return "", gerr.New(gerr.Internal, "unhandled legacy statement")
	}
}

func whereClause(alias string, w *query.Where) string {
	if w == nil {
		return ""
	}
	return " WHERE " + alias + "." + quoteIdent(w.Key) + " " + w.Op + " " + formatValue(w.Value)
}

func formatProps(props map[string]any) string {
	if len(props) == 0 {
		return ""
	}
	return " " + mapLit(props)
}

func mapLit(props map[string]any) string {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, quoteIdent(k)+": "+formatValue(props[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func formatValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float64:
		return value.Format(value.Float(x))
	case string:
		return strconv.Quote(x)
	default:
		return strconv.Quote(fmt.Sprint(x))
	}
}

func quoteIdent(s string) string {
	if s != "" && !lexer.IsKeyword(s) && isBareIdent(s) {
		return s
	}
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

func isBareIdent(s string) bool {
	for i, r := range s {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return s != ""
}

func looksLegacy(src string) bool {
	fields := strings.Fields(src)
	if len(fields) == 0 {
		return false
	}
	first := strings.ToUpper(fields[0])
	switch first {
	case "GET", "EDGES", "NEIGHBORS", "PATH", "UPDATE", "SAVE", "LOAD", "HELP", "EXIT", "QUIT":
		return true
	case "CREATE", "MATCH", "DELETE":
		if len(fields) < 2 {
			return false
		}
		second := strings.ToUpper(fields[1])
		return second == "NODE" || second == "EDGE"
	default:
		return false
	}
}
