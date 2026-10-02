// Package gobimport reads a legacy gob snapshot and text WAL into a disk database.
package gobimport

import (
	"bufio"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
	"github.com/shekhar8352/mini-graph-db/internal/query"
	"github.com/shekhar8352/mini-graph-db/internal/storage/disk"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// snapshotVersion is the newest gob snapshot this importer reads.
// Version 0 is a single label and scalar properties.
// Version 1 adds a label set and value.EncodeRecord.
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

// Import reads a legacy gob snapshot and, when walPath is set, replays that
// text WAL of query lines. The result is written to a new disk database in dest.
// dest must not already contain a db file.
func Import(snapshotPath, walPath, dest string) error {
	if snapshotPath == "" {
		return gerr.New(gerr.InvalidArgument, "legacy snapshot path is empty")
	}
	if dest == "" {
		return gerr.New(gerr.InvalidArgument, "destination directory is empty")
	}
	snap, err := readSnapshot(snapshotPath)
	if err != nil {
		return err
	}
	lines, err := readTextWAL(walPath)
	if err != nil {
		return err
	}
	g := graph.New()
	if err := g.Import(snap); err != nil {
		return err
	}
	exec := query.NewExecutor(g)
	for _, line := range lines {
		if _, err := exec.ExecString(line); err != nil {
			return fmt.Errorf("replay %q: %w", line, err)
		}
	}
	return writeDisk(dest, g.Export())
}

func readSnapshot(path string) (graph.Snapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return graph.Snapshot{}, gerr.Wrap(gerr.NotFound, path, err)
		}
		return graph.Snapshot{}, gerr.Wrap(gerr.Unavailable, "open legacy snapshot", err)
	}
	defer func() { _ = f.Close() }()

	var enc encodedSnapshot
	if err := gob.NewDecoder(f).Decode(&enc); err != nil {
		return graph.Snapshot{}, gerr.Wrap(gerr.Corruption, "legacy snapshot", err)
	}
	if enc.Version > snapshotVersion {
		return graph.Snapshot{}, gerr.Newf(gerr.InvalidArgument, "snapshot format version %d is newer than supported version %d", enc.Version, snapshotVersion)
	}
	snap := graph.Snapshot{
		NextNode: enc.NextNode,
		NextEdge: enc.NextEdge,
		PropKeys: enc.PropKeys,
	}
	for _, n := range enc.Nodes {
		props, err := decodeProps(n.Props)
		if err != nil {
			return graph.Snapshot{}, err
		}
		snap.Nodes = append(snap.Nodes, graph.MakeNode(n.ID, nodeLabels(enc.Version, n), props))
	}
	for _, e := range enc.Edges {
		props, err := decodeProps(e.Props)
		if err != nil {
			return graph.Snapshot{}, err
		}
		snap.Edges = append(snap.Edges, graph.MakeEdge(e.ID, e.From, e.To, e.Label, props))
	}
	return snap, nil
}

func nodeLabels(version int, n encodedNode) []string {
	if version == 0 {
		return []string{n.Label}
	}
	return n.Labels
}

func decodeProps(ps []encodedProp) ([]graph.Prop, error) {
	out := make([]graph.Prop, 0, len(ps))
	for _, p := range ps {
		prop := graph.Prop{ID: p.KeyID, Name: p.Key}
		if len(p.Record) > 0 {
			v, err := value.DecodeRecord(p.Record)
			if err != nil {
				return nil, gerr.Wrap(gerr.Corruption, "property "+p.Key, err)
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

func readTextWAL(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, gerr.Wrap(gerr.NotFound, path, err)
		}
		return nil, gerr.Wrap(gerr.Unavailable, "open legacy wal", err)
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
	if err := sc.Err(); err != nil {
		return nil, gerr.Wrap(gerr.Unavailable, "read legacy wal", err)
	}
	return lines, nil
}

func writeDisk(dest string, snap graph.Snapshot) error {
	dbPath := filepath.Join(dest, "db")
	if _, err := os.Stat(dbPath); err == nil {
		return gerr.New(gerr.AlreadyExists, "database directory already contains db")
	} else if err != nil && !os.IsNotExist(err) {
		return gerr.Wrap(gerr.Unavailable, "stat database", err)
	}
	_, statErr := os.Stat(dest)
	created := os.IsNotExist(statErr)
	eng, err := disk.OpenEngine(dest, disk.EngineOptions{
		CheckpointInterval: -1,
		CheckpointBytes:    -1,
	})
	if err != nil {
		if created {
			_ = os.RemoveAll(dest)
		}
		return err
	}
	if err := graph.NewWith(eng).Import(snap); err != nil {
		_ = eng.Close()
		removeDest(dest, created)
		return err
	}
	if err := eng.Close(); err != nil {
		return err
	}
	return nil
}

func removeDest(dest string, created bool) {
	if created {
		_ = os.RemoveAll(dest)
		return
	}
	_ = os.Remove(filepath.Join(dest, "db"))
	_ = os.RemoveAll(filepath.Join(dest, "wal"))
}
