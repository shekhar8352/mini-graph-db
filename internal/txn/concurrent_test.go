package txn

import (
	"sync"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
	"github.com/shekhar8352/mini-graph-db/internal/storage/memory"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func TestConcurrentGraphInvariants(t *testing.T) {
	m, err := Open(memory.Open(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	g := graph.NewWith(m)
	seed, err := g.CreateNode([]string{"counter"}, map[string]value.Value{"n": value.Int(0)})
	if err != nil {
		t.Fatal(err)
	}

	const writers = 4
	const readers = 4
	const each = 20
	stop := make(chan struct{})
	var wg sync.WaitGroup
	errCh := make(chan error, writers+readers)

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			view := graph.NewWith(m)
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := readInvariant(view); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	var commits sync.WaitGroup
	commits.Add(writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer commits.Done()
			view := graph.NewWith(m)
			for n := 0; n < each; n++ {
				if err := writePair(view); err != nil {
					errCh <- err
					return
				}
				if err := bump(view, seed.ID); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	commits.Wait()
	close(stop)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	got, ok := g.GetNode(seed.ID)
	if !ok {
		t.Fatal("counter missing")
	}
	v, ok := got.Prop("n")
	if !ok {
		t.Fatal("counter prop")
	}
	n, ok := v.IntValue()
	if !ok || n != int64(writers*each) {
		t.Fatalf("counter %d want %d", n, writers*each)
	}
	nodes := g.AllNodes()
	edges := g.AllEdges()
	ids := map[uint64]struct{}{}
	for _, node := range nodes {
		ids[node.ID] = struct{}{}
	}
	for _, e := range edges {
		if _, ok := ids[e.From]; !ok {
			t.Fatalf("edge %d missing from %d", e.ID, e.From)
		}
		if _, ok := ids[e.To]; !ok {
			t.Fatalf("edge %d missing to %d", e.ID, e.To)
		}
	}
}

func readInvariant(g *graph.Graph) error {
	if err := g.Begin(true); err != nil {
		return err
	}
	nodes := g.AllNodes()
	edges := g.AllEdges()
	ids := map[uint64]struct{}{}
	for _, n := range nodes {
		ids[n.ID] = struct{}{}
	}
	for _, e := range edges {
		if _, ok := ids[e.From]; !ok {
			_ = g.Rollback()
			return gerr.Newf(gerr.Internal, "edge %d from %d missing in snapshot", e.ID, e.From)
		}
		if _, ok := ids[e.To]; !ok {
			_ = g.Rollback()
			return gerr.Newf(gerr.Internal, "edge %d to %d missing in snapshot", e.ID, e.To)
		}
	}
	var counter int64
	for _, n := range nodes {
		if v, ok := n.Prop("n"); ok {
			counter, _ = v.IntValue()
		}
	}
	again := g.AllNodes()
	var counter2 int64
	for _, n := range again {
		if v, ok := n.Prop("n"); ok {
			counter2, _ = v.IntValue()
		}
	}
	if counter != counter2 || len(nodes) != len(again) {
		_ = g.Rollback()
		return gerr.New(gerr.Internal, "snapshot changed inside one transaction")
	}
	return g.Rollback()
}

func writePair(g *graph.Graph) error {
	for {
		if err := g.Begin(false); err != nil {
			return err
		}
		a, err := g.CreateNode([]string{"person"}, nil)
		if err != nil {
			_ = g.Rollback()
			return err
		}
		b, err := g.CreateNode([]string{"person"}, nil)
		if err != nil {
			_ = g.Rollback()
			return err
		}
		if _, err := g.CreateEdge(a.ID, b.ID, "KNOWS", nil); err != nil {
			_ = g.Rollback()
			return err
		}
		err = g.Commit()
		if err == nil {
			return nil
		}
		_ = g.Rollback()
		if gerr.IsCode(err, gerr.Conflict) {
			continue
		}
		return err
	}
}

func bump(g *graph.Graph, id uint64) error {
	for {
		if err := g.Begin(false); err != nil {
			return err
		}
		n, ok := g.GetNode(id)
		if !ok {
			_ = g.Rollback()
			return gerr.New(gerr.NotFound, "counter")
		}
		cur, _ := n.Prop("n")
		i, _ := cur.IntValue()
		if _, err := g.UpdateNodeValues(id, map[string]value.Value{"n": value.Int(i + 1)}); err != nil {
			_ = g.Rollback()
			if gerr.IsCode(err, gerr.Conflict) {
				continue
			}
			return err
		}
		err := g.Commit()
		if err == nil {
			return nil
		}
		_ = g.Rollback()
		if gerr.IsCode(err, gerr.Conflict) {
			continue
		}
		return err
	}
}
