package lexer

import (
	"strings"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

func FuzzScan(f *testing.F) {
	for _, src := range []string{
		`MATCH (a:Person {name: "Alice"}) RETURN a.name`,
		`"\n\x41\u0042"`,
		"`a``b`",
		`$from`,
		`$` + "`match`",
		`0x7fffffffffffffff`,
		`1.5e-2`,
		"-->",
		"<--",
		"-- c\n/* b */",
		"\r\n",
		`date("2025-01-31")`,
		`duration("P1DT2H")`,
		string([]byte{0xff, 0xfe}),
		"",
		"`",
		`"`,
		"/*",
		"1e",
		"*1..3",
	} {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		toks, err := Scan(src)
		again, err2 := Scan(src)
		if (err == nil) != (err2 == nil) || len(toks) != len(again) {
			t.Fatalf("unstable err %v / %v lens %d %d", err, err2, len(toks), len(again))
		}
		if err != nil {
			if gerr.CodeOf(err) != gerr.Syntax {
				t.Fatalf("code %v", err)
			}
			if !strings.Contains(err.Error(), " at ") {
				t.Fatalf("position %v", err)
			}
			return
		}
		if len(toks) == 0 || toks[len(toks)-1].Kind != EOF {
			t.Fatalf("missing EOF")
		}
		prev := -1
		for _, tok := range toks {
			if tok.Line < 1 || tok.Col < 1 {
				t.Fatalf("pos %+v", tok)
			}
			if tok.Offset < 0 || tok.Offset > len(src) {
				t.Fatalf("offset %+v len %d", tok, len(src))
			}
			if tok.Offset < prev {
				t.Fatalf("offset went backwards %+v", tok)
			}
			prev = tok.Offset
		}
	})
}
