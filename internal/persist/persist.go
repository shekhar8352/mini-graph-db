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
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func init() {
	gob.Register(int64(0))
	gob.Register(float64(0))
	gob.Register("")
	gob.Register(false)
	gob.Register(encodedProp{})
	gob.Register([]encodedProp{})
}

// snapshotVersion is the gob snapshot format written by this binary.
// Version 0 is the Phase 0 layout (single label, scalar properties).
// Version 1 adds a label set, interned property-key ids, and value.EncodeRecord.
// A newer version is rejected.
const snapshotVersion = 1

type encodedProp struct {
	Key    string
	KeyID  uint32
	Kind   string
	Str    string
	Int    int64
	Float  float64
	Bool   bool
	Record []byte
}

type encodedNode struct {
	ID     uint64
	Label  string
	Labels []string
	Props  []encodedProp
}

type encodedEdge struct {
	ID    uint64
	From  uint64
	To    uint64
	Label string
	Props []encodedProp
}

type encodedSnapshot struct {
	Version  int
	Nodes    []encodedNode
	Edges    []encodedEdge
	NextNode uint64
	NextEdge uint64
	PropKeys []string
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
		Version:  snapshotVersion,
		NextNode: snap.NextNode,
		NextEdge: snap.NextEdge,
		PropKeys: snap.PropKeys,
	}
	for _, n := range snap.Nodes {
		labels := n.Labels()
		node := encodedNode{ID: n.ID, Labels: labels, Props: encodeProps(n.PropList())}
		if len(labels) > 0 {
			node.Label = labels[0]
		}
		enc.Nodes = append(enc.Nodes, node)
	}
	for _, e := range snap.Edges {
		enc.Edges = append(enc.Edges, encodedEdge{
			ID: e.ID, From: e.From, To: e.To, Label: e.Label, Props: encodeProps(e.PropList()),
		})
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
	if enc.Version > snapshotVersion {
		return fmt.Errorf("persist: snapshot format version %d is newer than supported version %d", enc.Version, snapshotVersion)
	}
	snap := graph.Snapshot{
		NextNode: enc.NextNode,
		NextEdge: enc.NextEdge,
		PropKeys: enc.PropKeys,
	}
	for _, n := range enc.Nodes {
		props, err := decodeProps(n.Props)
		if err != nil {
			return err
		}
		snap.Nodes = append(snap.Nodes, graph.MakeNode(n.ID, nodeLabels(enc.Version, n), props))
	}
	for _, e := range enc.Edges {
		props, err := decodeProps(e.Props)
		if err != nil {
			return err
		}
		snap.Edges = append(snap.Edges, graph.MakeEdge(e.ID, e.From, e.To, e.Label, props))
	}
	g.Import(snap)
	return nil
}

func nodeLabels(version int, n encodedNode) []string {
	if version == 0 {
		return []string{n.Label}
	}
	return n.Labels
}

func encodeProps(props []graph.Prop) []encodedProp {
	out := make([]encodedProp, 0, len(props))
	for _, p := range props {
		ep := encodedProp{
			Key:    p.Name,
			KeyID:  p.ID,
			Kind:   "r",
			Record: value.EncodeRecord(p.Value),
		}
		switch p.Value.Kind() {
		case value.KindString:
			ep.Str, _ = p.Value.StringValue()
		case value.KindInt:
			ep.Int, _ = p.Value.IntValue()
		case value.KindFloat:
			ep.Float, _ = p.Value.FloatValue()
		case value.KindBool:
			ep.Bool, _ = p.Value.BoolValue()
		}
		out = append(out, ep)
	}
	return out
}

func decodeProps(ps []encodedProp) ([]graph.Prop, error) {
	out := make([]graph.Prop, 0, len(ps))
	for _, p := range ps {
		prop := graph.Prop{ID: p.KeyID, Name: p.Key}
		if len(p.Record) > 0 {
			v, err := value.DecodeRecord(p.Record)
			if err != nil {
				return nil, fmt.Errorf("persist: property %q: %w", p.Key, err)
			}
			prop.Value = v
			out = append(out, prop)
			continue
		}
		switch p.Kind {
		case "i":
			prop.Value = value.Int(p.Int)
		case "f":
			prop.Value = value.Float(p.Float)
		case "b":
			prop.Value = value.Bool(p.Bool)
		default:
			prop.Value = value.String(p.Str)
		}
		out = append(out, prop)
	}
	return out, nil
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
