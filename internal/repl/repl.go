// Package repl is the embedded interactive shell.
// Each line is compiled as GQL-lite, or rewritten from the legacy line
// language, and then run by the v2 executor.
package repl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/peterh/liner"

	"github.com/shekhar8352/mini-graph-db/internal/compat/legacy"
	"github.com/shekhar8352/mini-graph-db/internal/exec"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
	"github.com/shekhar8352/mini-graph-db/internal/storage/memory"
	"github.com/shekhar8352/mini-graph-db/internal/txn"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// Config controls one REPL session. The graph lives in memory for the session.
type Config struct {
	HistoryPath string
	In          io.Reader
	Out         io.Writer
}

// Run starts the interactive loop. It returns when the user exits or input ends.
func Run(cfg Config) error {
	if cfg.In == nil {
		cfg.In = os.Stdin
	}
	if cfg.Out == nil {
		cfg.Out = os.Stdout
	}

	eng := memory.Open()
	mgr, err := txn.Open(eng, txn.Options{})
	if err != nil {
		_ = eng.Close()
		return err
	}
	defer func() { _ = mgr.Close() }()
	g := graph.NewWith(mgr)

	fmt.Fprintln(cfg.Out, "mini-graph-db  type HELP for commands")
	if isTerminal(cfg.In) {
		return runLineEditor(cfg, g)
	}
	return runScanner(cfg, g)
}

func isTerminal(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	return liner.TerminalSupported() && f.Fd() == os.Stdin.Fd()
}

func runLineEditor(cfg Config, g *graph.Graph) error {
	ed := liner.NewLiner()
	defer func() { _ = ed.Close() }()
	ed.SetCtrlCAborts(true)
	ed.SetMultiLineMode(false)
	ed.SetTabCompletionStyle(liner.TabPrints)
	ed.SetCompleter(commandCompleter)

	if cfg.HistoryPath != "" {
		if f, err := os.Open(cfg.HistoryPath); err == nil {
			_, _ = ed.ReadHistory(f)
			_ = f.Close()
		}
	}

	for {
		line, err := ed.Prompt("graph> ")
		if err == liner.ErrPromptAborted {
			fmt.Fprintln(cfg.Out)
			continue
		}
		if err == io.EOF {
			fmt.Fprintln(cfg.Out)
			break
		}
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		ed.AppendHistory(line)
		if cfg.HistoryPath != "" {
			if f, err := os.Create(cfg.HistoryPath); err == nil {
				_, _ = ed.WriteHistory(f)
				_ = f.Close()
			}
		}
		if err := handleLine(cfg.Out, g, line); err != nil {
			if errors.Is(err, errExit) {
				return nil
			}
			slog.Error("repl failed", "err", err)
			return err
		}
	}
	return nil
}

func runScanner(cfg Config, g *graph.Graph) error {
	sc := bufio.NewScanner(cfg.In)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)

	for {
		fmt.Fprint(cfg.Out, "graph> ")
		if !sc.Scan() {
			fmt.Fprintln(cfg.Out)
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if err := handleLine(cfg.Out, g, line); err != nil {
			if errors.Is(err, errExit) {
				return nil
			}
			slog.Error("repl failed", "err", err)
			return err
		}
	}
	return sc.Err()
}

func handleLine(out io.Writer, g *graph.Graph, line string) error {
	compiled, err := legacy.Compile(line)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return nil
	}
	if compiled.Help {
		fmt.Fprint(out, helpText)
		return nil
	}
	if compiled.Exit {
		fmt.Fprintln(out, "bye")
		return errExit
	}
	if compiled.Source == "" && (compiled.Script == nil || len(compiled.Script.Stmts) == 0) {
		return nil
	}
	opt := exec.Options{}
	if compiled.Legacy {
		opt.Notifications = []exec.Notification{{
			Code:    exec.DeprecatedSyntax,
			Message: compiled.Message,
		}}
	}
	res, err := exec.Run(context.Background(), g, compiled.Source, opt)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return nil
	}
	printResult(out, g, res)
	return nil
}

var errExit = errors.New("exit")

func commandCompleter(line string) []string {
	cmds := []string{
		"MATCH (",
		"OPTIONAL MATCH (",
		"CREATE (",
		"MERGE (",
		"RETURN ",
		"WITH ",
		"UNWIND ",
		"EXPLAIN ",
		"PROFILE ",
		"SHOW STATS",
		"SHOW LABELS",
		"SHOW EDGE TYPES",
		"SHOW PROPERTY KEYS",
		"BEGIN",
		"BEGIN READ ONLY",
		"COMMIT",
		"ROLLBACK",
		"VACUUM",
		"CREATE NODE ",
		"CREATE EDGE ",
		"UPDATE NODE ",
		"UPDATE EDGE ",
		"MATCH ",
		"MATCH EDGE ",
		"GET NODE ",
		"GET EDGE ",
		"EDGES ",
		"NEIGHBORS ",
		"PATH ",
		"DELETE NODE ",
		"DELETE EDGE ",
		"HELP",
		"EXIT",
	}
	upper := strings.ToUpper(line)
	var out []string
	for _, c := range cmds {
		if strings.HasPrefix(strings.ToUpper(c), upper) {
			out = append(out, c)
		}
	}
	return out
}

func printResult(w io.Writer, g *graph.Graph, res *exec.Result) {
	if res == nil {
		return
	}
	if isStats(res) {
		printStats(w, res)
		return
	}
	if len(res.Columns) == 0 {
		printSummary(w, res.Summary)
		return
	}
	rows := res.All()
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no rows)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(res.Columns, "\t"))
	for _, row := range rows {
		cells := make([]string, len(res.Columns))
		for i := range res.Columns {
			var v value.Value
			if i < len(row) {
				v = row[i]
			}
			cells[i] = formatCell(g, v)
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	_ = tw.Flush()
}

func isStats(res *exec.Result) bool {
	return len(res.Columns) == 3 && res.Columns[0] == "nodes" && res.Columns[1] == "edges" && res.Columns[2] == "labels"
}

func printStats(w io.Writer, res *exec.Result) {
	var nodes, edges int64
	var labels []string
	if rows := res.All(); len(rows) > 0 && len(rows[0]) >= 3 {
		nodes, _ = rows[0][0].IntValue()
		edges, _ = rows[0][1].IntValue()
		if xs, ok := rows[0][2].ListValue(); ok {
			for _, v := range xs {
				s, _ := v.StringValue()
				labels = append(labels, s)
			}
		}
	}
	fmt.Fprintf(w, "nodes:  %d\nedges:  %d\nlabels: %s\n", nodes, edges, strings.Join(labels, ", "))
}

func printSummary(w io.Writer, s exec.Summary) {
	var parts []string
	if s.NodesCreated > 0 {
		parts = append(parts, fmt.Sprintf("created %d node", s.NodesCreated))
	}
	if s.EdgesCreated > 0 {
		parts = append(parts, fmt.Sprintf("created %d edge", s.EdgesCreated))
	}
	if s.NodesDeleted > 0 {
		parts = append(parts, fmt.Sprintf("deleted %d node", s.NodesDeleted))
	}
	if s.EdgesDeleted > 0 {
		parts = append(parts, fmt.Sprintf("deleted %d edge", s.EdgesDeleted))
	}
	if s.PropertiesSet > 0 {
		parts = append(parts, fmt.Sprintf("set %d property", s.PropertiesSet))
	}
	if s.LabelsAdded > 0 {
		parts = append(parts, fmt.Sprintf("added %d label", s.LabelsAdded))
	}
	if s.LabelsRemoved > 0 {
		parts = append(parts, fmt.Sprintf("removed %d label", s.LabelsRemoved))
	}
	if s.ConstraintsAdded > 0 {
		parts = append(parts, fmt.Sprintf("added %d constraint", s.ConstraintsAdded))
	}
	if s.ConstraintsRemoved > 0 {
		parts = append(parts, fmt.Sprintf("removed %d constraint", s.ConstraintsRemoved))
	}
	if len(parts) == 0 {
		return
	}
	fmt.Fprintln(w, strings.Join(parts, ", "))
}

func formatCell(g *graph.Graph, v value.Value) string {
	if id, ok := v.NodeID(); ok {
		if n, found := g.GetNode(id); found {
			return formatNode(n)
		}
	}
	if id, ok := v.EdgeID(); ok {
		if e, found := g.GetEdge(id); found {
			return formatEdge(e)
		}
	}
	return value.Format(v)
}

func formatNode(n graph.Node) string {
	return fmt.Sprintf("%d:%s {%s}", n.ID, strings.Join(n.Labels(), ","), formatProps(n.Properties()))
}

func formatEdge(e graph.Edge) string {
	return fmt.Sprintf("%d %d -%s-> %d {%s}", e.ID, e.From, e.Label, e.To, formatProps(e.Properties()))
}

func formatProps(p map[string]value.Value) string {
	if len(p) == 0 {
		return ""
	}
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+value.Format(p[k]))
	}
	return strings.Join(parts, " ")
}

const helpText = `GQL-lite (grammar version 1):
  MATCH (n:Label) WHERE ... RETURN n
  CREATE (a:Person {name: "Ada"})-[:KNOWS]->(b:Person {name: "Bea"})
  MERGE (n:Person {name: $name}) ON CREATE SET n.seen = 1
  EXPLAIN <query>   PROFILE <query>
  SHOW STATS        SHOW LABELS
  BEGIN [READ ONLY]   COMMIT   ROLLBACK   VACUUM

Legacy line syntax is rewritten and still runs:
  CREATE NODE <label> {key: value, ...}
  CREATE EDGE <from> -<LABEL>-> <to> {key: value, ...}
  UPDATE NODE <id> {key: value, ...}
  UPDATE EDGE <id> {key: value, ...}
  MATCH <label> [WHERE <key> <op> <value>]
  MATCH EDGE <label> [WHERE <key> <op> <value>]
  GET NODE <id>
  GET EDGE <id>
  EDGES <from> TO <to>
  NEIGHBORS <id> [DEPTH <n>]
  PATH <from> TO <to>
  DELETE NODE <id>
  DELETE EDGE <id>
  HELP
  EXIT
`
