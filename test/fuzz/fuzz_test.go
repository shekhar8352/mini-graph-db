// Package fuzz hosts the Phase 4 fuzz suite.
// Native FuzzXxx targets also live next to the parser and executor.
package fuzz

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/exec"
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/lang/parser"
)

func FuzzParse(f *testing.F) {
	for _, src := range []string{
		``,
		`MATCH (n:Person) RETURN n`,
		`RETURN -2^2`,
		`BEGIN READ ONLY`,
		`CREATE (a:Person {name: "Ada"})-[k:KNOWS*1..3]->(b)`,
	} {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		tree, err := parser.Parse(src)
		if err != nil {
			if gerr.CodeOf(err) == "" {
				t.Fatalf("error without a code: %v", err)
			}
			return
		}
		if _, err := parser.Parse(ast.Format(tree)); err != nil {
			t.Fatalf("format %q from %q: %v", ast.Format(tree), src, err)
		}
	})
}

func FuzzExec(f *testing.F) {
	for _, src := range []string{
		`RETURN 1 AS n`,
		`MATCH (n) RETURN n`,
		`CREATE (a:Person)`,
		`UNWIND [1, 2] AS x RETURN x`,
		`SHOW STATS`,
	} {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if _, err := parser.Parse(src); err != nil {
			if gerr.CodeOf(err) == "" {
				t.Fatalf("parse error without a code: %v", err)
			}
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, err := exec.Run(ctx, graph.New(), src, exec.Options{Budget: 1 << 16, MaxHops: 4})
		if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return
		}
		if gerr.CodeOf(err) == "" {
			t.Fatalf("untyped exec error from %q: %v", src, err)
		}
	})
}
