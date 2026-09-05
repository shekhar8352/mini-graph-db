package repl

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/peterh/liner"

	"mini-graph-db/internal/graph"
	"mini-graph-db/internal/persist"
	"mini-graph-db/internal/query"
)

// Config controls snapshot/WAL recovery for a REPL session.
type Config struct {
	DBPath      string
	WALPath     string
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

	g := graph.New()
	if cfg.DBPath != "" {
		if _, err := os.Stat(cfg.DBPath); err == nil {
			if err := persist.Load(g, cfg.DBPath); err != nil {
				return fmt.Errorf("load snapshot: %w", err)
			}
			fmt.Fprintf(cfg.Out, "loaded snapshot %s\n", cfg.DBPath)
		}
	}

	var wal *persist.WAL
	if cfg.WALPath != "" {
		var err error
		wal, err = persist.OpenWAL(cfg.WALPath)
		if err != nil {
			return fmt.Errorf("open wal: %w", err)
		}
		defer wal.Close()
	}

	exec := query.NewExecutor(g, wal)

	if cfg.WALPath != "" {
		lines, err := persist.ReadWAL(cfg.WALPath)
		if err != nil {
			return fmt.Errorf("read wal: %w", err)
		}
		for _, line := range lines {
			if _, err := exec.ExecString(line, true); err != nil {
				return fmt.Errorf("replay %q: %w", line, err)
			}
		}
		if len(lines) > 0 {
			fmt.Fprintf(cfg.Out, "replayed %d wal statement(s)\n", len(lines))
		}
	}

	fmt.Fprintln(cfg.Out, "mini-graph-db  type HELP for commands")
	if isTerminal(cfg.In) {
		return runLineEditor(cfg, exec)
	}
	return runScanner(cfg, exec)
}

func isTerminal(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	return liner.TerminalSupported() && f.Fd() == os.Stdin.Fd()
}

func runLineEditor(cfg Config, exec *query.Executor) error {
	ed := liner.NewLiner()
	defer ed.Close()
	ed.SetCtrlCAborts(true)
	ed.SetMultiLineMode(false)
	ed.SetTabCompletionStyle(liner.TabPrints)
	ed.SetCompleter(commandCompleter)

	if cfg.HistoryPath != "" {
		if f, err := os.Open(cfg.HistoryPath); err == nil {
			_, _ = ed.ReadHistory(f)
			f.Close()
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
				f.Close()
			}
		}
		if err := handleLine(cfg.Out, exec, line); err != nil {
			if errors.Is(err, errExit) {
				return nil
			}
			return err
		}
	}
	return nil
}

func runScanner(cfg Config, exec *query.Executor) error {
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
		if err := handleLine(cfg.Out, exec, line); err != nil {
			if errors.Is(err, errExit) {
				return nil
			}
			return err
		}
	}
	return sc.Err()
}

func handleLine(out io.Writer, exec *query.Executor, line string) error {
	res, err := exec.ExecString(line, false)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return nil
	}
	printResult(out, res)
	if res.Exit {
		return errExit
	}
	return nil
}

var errExit = errors.New("exit")

func commandCompleter(line string) []string {
	cmds := []string{
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
		"SHOW STATS",
		"SAVE ",
		"LOAD ",
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

func printResult(w io.Writer, res query.Result) {
	switch res.Kind {
	case "nodes":
		printNodes(w, res.Nodes)
	case "edges":
		printEdges(w, res.Edges)
	case "neighbors":
		printNeighbors(w, res.Neighbors)
	case "path":
		if len(res.Path) == 0 {
			fmt.Fprintln(w, res.Message)
			return
		}
		printNodes(w, res.Path)
		fmt.Fprintln(w, res.Message)
	case "stats":
		fmt.Fprintf(w, "nodes:  %d\nedges:  %d\nlabels: %s\n",
			res.Stats.Nodes, res.Stats.Edges, strings.Join(res.Stats.Labels, ", "))
	default:
		if res.Message != "" {
			fmt.Fprintln(w, res.Message)
		}
	}
}

func printNodes(w io.Writer, nodes []graph.Node) {
	if len(nodes) == 0 {
		fmt.Fprintln(w, "(no nodes)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tLABEL\tPROPS")
	for _, n := range nodes {
		fmt.Fprintf(tw, "%d\t%s\t%s\n", n.ID, n.Label, formatProps(n.Props))
	}
	tw.Flush()
}

func printEdges(w io.Writer, edges []graph.Edge) {
	if len(edges) == 0 {
		fmt.Fprintln(w, "(no edges)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tFROM\tTO\tLABEL\tPROPS")
	for _, e := range edges {
		fmt.Fprintf(tw, "%d\t%d\t%d\t%s\t%s\n", e.ID, e.From, e.To, e.Label, formatProps(e.Props))
	}
	tw.Flush()
}

func printNeighbors(w io.Writer, ns []graph.Neighbor) {
	if len(ns) == 0 {
		fmt.Fprintln(w, "(no neighbors)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "DEPTH\tID\tLABEL\tPROPS")
	for _, n := range ns {
		fmt.Fprintf(tw, "%d\t%d\t%s\t%s\n", n.Depth, n.Node.ID, n.Node.Label, formatProps(n.Node.Props))
	}
	tw.Flush()
}

func formatProps(p map[string]any) string {
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
		parts = append(parts, fmt.Sprintf("%s=%v", k, p[k]))
	}
	return strings.Join(parts, " ")
}
