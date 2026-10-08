package parser

import (
	"strings"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
)

func TestErrorRecovery(t *testing.T) {
	_, err := Parse("BEGIN READ;\nRETURN a < b < c;\nRETURN 1")
	if gerr.CodeOf(err) != gerr.Syntax {
		t.Fatalf("code %s err %v", gerr.CodeOf(err), err)
	}
	msg := err.Error()
	for _, want := range []string{"expected ONLY at 1:", "expected expression at 2:"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missing %q in %s", want, msg)
		}
	}
	if strings.Count(msg, " at ") != 2 {
		t.Fatalf("want two diagnostics, got %s", msg)
	}
}

func TestPrattPrecedence(t *testing.T) {
	cases := []struct{ in, out string }{
		{`RETURN NOT a AND b`, `RETURN NOT a AND b`},
		{`RETURN a OR b AND c`, `RETURN a OR b AND c`},
		{`RETURN -2^2`, `RETURN -2 ^ 2`},
		{`RETURN 2^3^4`, `RETURN 2 ^ 3 ^ 4`},
		{`RETURN 2^-2`, `RETURN 2 ^ (-2)`},
		{`RETURN 1+2*3`, `RETURN 1 + 2 * 3`},
		{`RETURN (1+2)*3`, `RETURN (1 + 2) * 3`},
	}
	for _, tc := range cases {
		tree, err := Parse(tc.in)
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if got := ast.Format(tree); got != tc.out {
			t.Fatalf("%s\n got %s\nwant %s", tc.in, got, tc.out)
		}
	}
}
