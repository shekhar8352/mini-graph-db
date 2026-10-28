package exec

import (
	"context"
	"fmt"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/graph"
)

func BenchmarkIndexEquality(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			g := graph.New()
			ctx := context.Background()
			for i := 0; i < n; i++ {
				src := fmt.Sprintf(`CREATE (a:Person {k: %d})`, i)
				if _, err := Run(ctx, g, src, Options{}); err != nil {
					b.Fatal(err)
				}
			}
			if _, err := Run(ctx, g, `CREATE INDEX person_k FOR (p:Person) ON (p.k)`, Options{}); err != nil {
				b.Fatal(err)
			}
			q := fmt.Sprintf(`MATCH (p:Person {k: %d}) RETURN p.k`, n/2)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := Run(ctx, g, q, Options{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
