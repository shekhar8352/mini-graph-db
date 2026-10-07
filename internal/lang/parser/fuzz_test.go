package parser

import (
	"reflect"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
)

func FuzzParse(f *testing.F) {
	for _, src := range []string{
		``,
		`MATCH (n:Person) RETURN n`,
		`RETURN -2^2`,
		`RETURN 0x2A != "a"`,
		`MATCH (a)-[k*1..]->(b)`,
		`BEGIN READ ONLY`,
		`/* c */ -- x`,
		"MATCH (`match`) RETURN $`from`",
	} {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		tree, err := Parse(src)
		if err != nil {
			if gerr.CodeOf(err) == "" {
				t.Fatalf("error without a code: %v", err)
			}
			return
		}
		formatted := ast.Format(tree)
		again, err := Parse(formatted)
		if err != nil {
			t.Fatalf("format %q from %q: %v", formatted, src, err)
		}
		if !reflect.DeepEqual(tree, again) {
			t.Fatalf("round trip %q -> %q -> %q", src, formatted, ast.Format(again))
		}
	})
}
