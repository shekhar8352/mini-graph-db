// Package persist stores gob snapshots and a text write-ahead log.
package persist

import (
	"bufio"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/graph"
)

func init() {
	gob.Register(int64(0))
	gob.Register(float64(0))
	gob.Register("")
	gob.Register(false)
	gob.Register(encodedProp{})
	gob.Register([]encodedProp{})
}

type encodedProp struct {
	Key   string
	Kind  string
	Str   string
	Int   int64
	Float float64
	Bool  bool
}

type encodedNode struct {
	ID    uint64
	Label string
	Props []encodedProp
}

type encodedEdge struct {
	ID    uint64
	From  uint64
	To    uint64
	Label string
	Props []encodedProp
}

type encodedSnapshot struct {
	Nodes    []encodedNode
	Edges    []encodedEdge
	NextNode uint64
	NextEdge uint64
}

// Save writes a gob snapshot of g to path, creating parent directories as needed.
func Save(g *graph.Graph, path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	snap := g.Export()
	enc := encodedSnapshot{
		NextNode: snap.NextNode,
		NextEdge: snap.NextEdge,
	}
	for _, n := range snap.Nodes {
		enc.Nodes = append(enc.Nodes, encodedNode{ID: n.ID, Label: n.Label, Props: encodeProps(n.Props)})
	}
	for _, e := range snap.Edges {
		enc.Edges = append(enc.Edges, encodedEdge{ID: e.ID, From: e.From, To: e.To, Label: e.Label, Props: encodeProps(e.Props)})
	}
	return gob.NewEncoder(f).Encode(enc)
}

// Load replaces g with the snapshot stored at path.
func Load(g *graph.Graph, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	var enc encodedSnapshot
	if err := gob.NewDecoder(f).Decode(&enc); err != nil {
		return err
	}
	snap := graph.Snapshot{
		NextNode: enc.NextNode,
		NextEdge: enc.NextEdge,
	}
	for _, n := range enc.Nodes {
		snap.Nodes = append(snap.Nodes, graph.Node{ID: n.ID, Label: n.Label, Props: decodeProps(n.Props)})
	}
	for _, e := range enc.Edges {
		snap.Edges = append(snap.Edges, graph.Edge{ID: e.ID, From: e.From, To: e.To, Label: e.Label, Props: decodeProps(e.Props)})
	}
	g.Import(snap)
	return nil
}

func encodeProps(m map[string]any) []encodedProp {
	out := make([]encodedProp, 0, len(m))
	for k, v := range m {
		p := encodedProp{Key: k}
		switch t := v.(type) {
		case string:
			p.Kind, p.Str = "s", t
		case int64:
			p.Kind, p.Int = "i", t
		case int:
			p.Kind, p.Int = "i", int64(t)
		case float64:
			p.Kind, p.Float = "f", t
		case bool:
			p.Kind, p.Bool = "b", t
		default:
			p.Kind, p.Str = "s", fmt.Sprint(t)
		}
		out = append(out, p)
	}
	return out
}

func decodeProps(ps []encodedProp) map[string]any {
	out := make(map[string]any, len(ps))
	for _, p := range ps {
		switch p.Kind {
		case "i":
			out[p.Key] = p.Int
		case "f":
			out[p.Key] = p.Float
		case "b":
			out[p.Key] = p.Bool
		default:
			out[p.Key] = p.Str
		}
	}
	return out
}

// WAL is an append-only log of mutating query statements.
type WAL struct {
	path string
	f    *os.File
}

// OpenWAL appends to path, creating the file if needed.
func OpenWAL(path string) (*WAL, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &WAL{path: path, f: f}, nil
}

// Path returns the WAL file path.
func (w *WAL) Path() string { return w.path }

// Append writes one statement line and fsyncs.
func (w *WAL) Append(stmt string) error {
	if w == nil || w.f == nil {
		return nil
	}
	line := strings.TrimSpace(stmt)
	if line == "" {
		return nil
	}
	if _, err := fmt.Fprintln(w.f, line); err != nil {
		return err
	}
	return w.f.Sync()
}

// Truncate discards every logged statement.
func (w *WAL) Truncate() error {
	if w == nil || w.f == nil {
		return nil
	}
	if err := w.f.Truncate(0); err != nil {
		return err
	}
	_, err := w.f.Seek(0, 0)
	return err
}

// Close closes the underlying file.
func (w *WAL) Close() error {
	if w == nil || w.f == nil {
		return nil
	}
	return w.f.Close()
}

// ReadWAL returns the statements stored in a WAL file. Missing files yield nil.
func ReadWAL(path string) ([]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return lines, sc.Err()
}
