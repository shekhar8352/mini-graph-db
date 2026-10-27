package exec

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
)

func FuzzRun(f *testing.F) {
	for _, src := range []string{
		``,
		`RETURN 1`,
		`MATCH (n:Person) RETURN n`,
		`CREATE (a:Person {name: "Ada"})`,
		`UNWIND range(1, 3) AS x RETURN x`,
		`BEGIN READ ONLY`,
		`SHOW STATS`,
		`EXPLAIN RETURN 1 AS n`,
		`RETURN 1 / 0`,
		`RETURN $missing`,
		`MATCH (a)-[*]->(b) RETURN b`,
		`CREATE USER alice SET PASSWORD "x"`,
	} {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		g := graph.New()
		_, err := Run(ctx, g, src, Options{Budget: 1 << 16, MaxHops: 4, Rand: func() float64 { return 0.5 }})
		if err == nil {
			return
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return
		}
		if gerr.CodeOf(err) == "" {
			t.Fatalf("untyped error from %q: %v", src, err)
		}
	})
}
