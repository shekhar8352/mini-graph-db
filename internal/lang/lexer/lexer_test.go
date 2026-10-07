package lexer

import (
	"math"
	"strings"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

func TestKeywords(t *testing.T) {
	if KwYield-KwAdmin < 50 {
		t.Fatalf("keyword span %d", KwYield-KwAdmin)
	}
	for k := KwAdmin; k <= KwYield; k++ {
		name := k.String()
		if !k.Keyword() || strings.HasPrefix(name, "kind(") {
			t.Fatalf("unnamed keyword %d %q", k, name)
		}
		toks := mustScan(t, name)
		if toks[0].Kind != k || toks[0].Text != name {
			t.Fatalf("%s: %+v", name, toks[0])
		}
		low := mustScan(t, strings.ToLower(name))
		if low[0].Kind != k {
			t.Fatalf("lower %s: %+v", name, low[0])
		}
		quoted := mustScan(t, "`"+name+"`")
		if quoted[0].Kind != Ident || quoted[0].Text != name {
			t.Fatalf("quoted %s: %+v", name, quoted[0])
		}
	}
}

func TestScan(t *testing.T) {
	cases := []struct {
		src  string
		want []item
	}{
		{"", []item{{EOF, ""}}},
		{"Match (n:Person)", []item{
			{KwMatch, "MATCH"}, {LParen, "("}, {Ident, "n"}, {Colon, ":"}, {Ident, "Person"}, {RParen, ")"}, {EOF, ""},
		}},
		{"n.match", []item{{Ident, "n"}, {Dot, "."}, {KwMatch, "MATCH"}, {EOF, ""}}},
		{"`match`", []item{{Ident, "match"}, {EOF, ""}}},
		{"`a``b`", []item{{Ident, "a`b"}, {EOF, ""}}},
		{"````", []item{{Ident, "`"}, {EOF, ""}}},
		{"$from", []item{{Param, "from"}, {EOF, ""}}},
		{"$FROM", []item{{Param, "FROM"}, {EOF, ""}}},
		{"$`match`", []item{{Param, "match"}, {EOF, ""}}},
		{"$ from", []item{{Dollar, "$"}, {KwFrom, "FROM"}, {EOF, ""}}},
		{"$1", []item{{Dollar, "$"}, {Int, "1"}, {EOF, ""}}},
		{`date("2025-01-31")`, []item{
			{Ident, "date"}, {LParen, "("}, {String, "2025-01-31"}, {RParen, ")"}, {EOF, ""},
		}},
		{`datetime("2025-01-31T12:00:00Z")`, []item{
			{Ident, "datetime"}, {LParen, "("}, {String, "2025-01-31T12:00:00Z"}, {RParen, ")"}, {EOF, ""},
		}},
		{`duration("P1DT2H")`, []item{
			{Ident, "duration"}, {LParen, "("}, {String, "P1DT2H"}, {RParen, ")"}, {EOF, ""},
		}},
		{`"a\n\t\"\\"`, []item{{String, "a\n\t\"\\"}, {EOF, ""}}},
		{`"\x41\u0042\101"`, []item{{String, "ABA"}, {EOF, ""}}},
		{"`-- not`", []item{{Ident, "-- not"}, {EOF, ""}}},
		{`"-- not"`, []item{{String, "-- not"}, {EOF, ""}}},
		{"0x2A", []item{{Int, "0x2A"}, {EOF, ""}}},
		{"0X2a", []item{{Int, "0X2a"}, {EOF, ""}}},
		{"010", []item{{Int, "010"}, {EOF, ""}}},
		{"1.5", []item{{Float, "1.5"}, {EOF, ""}}},
		{"1.5e2", []item{{Float, "1.5e2"}, {EOF, ""}}},
		{"1e+2", []item{{Float, "1e+2"}, {EOF, ""}}},
		{"1E-2", []item{{Float, "1E-2"}, {EOF, ""}}},
		{"1.", []item{{Int, "1"}, {Dot, "."}, {EOF, ""}}},
		{"1.e2", []item{{Int, "1"}, {Dot, "."}, {Ident, "e2"}, {EOF, ""}}},
		{"1..2", []item{{Int, "1"}, {DotDot, ".."}, {Int, "2"}, {EOF, ""}}},
		{"*1..3", []item{{Star, "*"}, {Int, "1"}, {DotDot, ".."}, {Int, "3"}, {EOF, ""}}},
		{"0x1.5", []item{{Int, "0x1"}, {Dot, "."}, {Int, "5"}, {EOF, ""}}},
		{"0x1e2", []item{{Int, "0x1e2"}, {EOF, ""}}},
		{"-->", []item{{Minus, "-"}, {Arrow, "->"}, {EOF, ""}}},
		{"<--", []item{{LeftArrow, "<-"}, {Minus, "-"}, {EOF, ""}}},
		{"(a)- -(b)", []item{
			{LParen, "("}, {Ident, "a"}, {RParen, ")"}, {Minus, "-"}, {Minus, "-"},
			{LParen, "("}, {Ident, "b"}, {RParen, ")"}, {EOF, ""},
		}},
		{".. <> != <= >= :: -> <- +=", []item{
			{DotDot, ".."}, {NotEq, "<>"}, {NotEq, "!="}, {LtEq, "<="}, {GtEq, ">="},
			{ColonColon, "::"}, {Arrow, "->"}, {LeftArrow, "<-"}, {PlusEq, "+="}, {EOF, ""},
		}},
		{"()[]{},:.;+-*/%^= < > | $", []item{
			{LParen, "("}, {RParen, ")"}, {LBracket, "["}, {RBracket, "]"},
			{LBrace, "{"}, {RBrace, "}"}, {Comma, ","}, {Colon, ":"}, {Dot, "."}, {Semi, ";"},
			{Plus, "+"}, {Minus, "-"}, {Star, "*"}, {Slash, "/"}, {Percent, "%"}, {Caret, "^"},
			{Eq, "="}, {Lt, "<"}, {Gt, ">"}, {Pipe, "|"}, {Dollar, "$"}, {EOF, ""},
		}},
		{"-- line comment\nMATCH", []item{{KwMatch, "MATCH"}, {EOF, ""}}},
		{"/* block */ RETURN", []item{{KwReturn, "RETURN"}, {EOF, ""}}},
		{"/* /* */ +", []item{{Plus, "+"}, {EOF, ""}}},
		{"/**/;", []item{{Semi, ";"}, {EOF, ""}}},
		{"-- /*\nMATCH", []item{{KwMatch, "MATCH"}, {EOF, ""}}},
		{"NaN Inf true FALSE null", []item{
			{KwNan, "NAN"}, {KwInf, "INF"}, {KwTrue, "TRUE"}, {KwFalse, "FALSE"}, {KwNull, "NULL"}, {EOF, ""},
		}},
		{"_a1", []item{{Ident, "_a1"}, {EOF, ""}}},
		{"1a", []item{{Int, "1"}, {Ident, "a"}, {EOF, ""}}},
	}
	for _, tc := range cases {
		toks := mustScan(t, tc.src)
		if len(toks) != len(tc.want) {
			t.Fatalf("%q: got %d tokens %v, want %d", tc.src, len(toks), kinds(toks), len(tc.want))
		}
		for i, want := range tc.want {
			if toks[i].Kind != want.kind || toks[i].Text != want.text {
				t.Fatalf("%q token %d: got %s %q, want %s %q", tc.src, i, toks[i].Kind, toks[i].Text, want.kind, want.text)
			}
		}
	}
}

func TestNumberValues(t *testing.T) {
	toks := mustScan(t, "0x2A 010 9223372036854775807 1.5e1 1e309 1e-9999")
	if toks[0].Int != 42 || toks[1].Int != 10 || toks[2].Int != 1<<63-1 {
		t.Fatalf("ints: %#v %#v %#v", toks[0], toks[1], toks[2])
	}
	if toks[3].Float != 15 {
		t.Fatalf("float %v", toks[3].Float)
	}
	if !math.IsInf(toks[4].Float, 1) {
		t.Fatalf("overflow %v", toks[4].Float)
	}
	if toks[5].Float != 0 {
		t.Fatalf("underflow %v", toks[5].Float)
	}
	if _, err := Scan("0x"); err == nil || gerr.CodeOf(err) != gerr.Syntax {
		t.Fatalf("0x: %v", err)
	}
	if _, err := Scan("9223372036854775808"); err == nil || !strings.Contains(err.Error(), "at 1:1") {
		t.Fatalf("overflow int: %v", err)
	}
	if _, err := Scan("0x8000000000000000"); err == nil {
		t.Fatal("hex past int64 scanned")
	}
}

func TestPositions(t *testing.T) {
	toks := mustScan(t, "ab\n  cd")
	if toks[1].Text != "cd" || toks[1].Line != 2 || toks[1].Col != 3 || toks[1].Offset != 5 {
		t.Fatalf("cd: %+v", toks[1])
	}
	toks = mustScan(t, "\r\nMATCH")
	if toks[0].Kind != KwMatch || toks[0].Line != 2 || toks[0].Col != 1 {
		t.Fatalf("crlf: %+v", toks[0])
	}
	toks = mustScan(t, "/*\r\n*/X")
	if toks[0].Text != "X" || toks[0].Line != 2 || toks[0].Col != 3 {
		t.Fatalf("block crlf: %+v", toks[0])
	}
	src := "éX"
	toks = mustScan(t, "`"+"é"+"`X")
	if toks[0].Text != "é" || toks[1].Text != "X" || toks[1].Col != 5 {
		t.Fatalf("multibyte: %+v %+v src %q", toks[0], toks[1], src)
	}
	if toks[0].At() != "1:1" {
		t.Fatal(toks[0].At())
	}
}

func TestSyntaxErrors(t *testing.T) {
	cases := []string{
		"\"unterminated",
		"\"line\nbreak\"",
		"\"\\q\"",
		"`",
		"``",
		"`a\nb`",
		"/* unterminated",
		"!",
		string([]byte{'o', 'k', 0xff}),
	}
	for _, src := range cases {
		_, err := Scan(src)
		if err == nil || gerr.CodeOf(err) != gerr.Syntax || !strings.Contains(err.Error(), " at ") {
			t.Fatalf("%q: %v", src, err)
		}
	}
	_, err := Scan(string([]byte{0xff}))
	if err == nil || !strings.Contains(err.Error(), "at 1:1") {
		t.Fatalf("utf-8: %v", err)
	}
	_, err = Scan("  \"\\q\"")
	if err == nil || !strings.Contains(err.Error(), "at 1:3") {
		t.Fatalf("escape pos: %v", err)
	}
}

func TestQueryFragment(t *testing.T) {
	src := "-- line comment\nMATCH (n:Person) /* block */ RETURN n.name; /* trailing */"
	toks := mustScan(t, src)
	want := []Kind{KwMatch, LParen, Ident, Colon, Ident, RParen, KwReturn, Ident, Dot, Ident, Semi, EOF}
	if len(toks) != len(want) {
		t.Fatalf("got %v", kinds(toks))
	}
	for i, k := range want {
		if toks[i].Kind != k {
			t.Fatalf("token %d: got %s want %s", i, toks[i].Kind, k)
		}
	}
	if toks[8].Kind != Dot || toks[9].Text != "name" {
		t.Fatalf("property: %+v %+v", toks[8], toks[9])
	}
}

type item struct {
	kind Kind
	text string
}

func mustScan(t *testing.T, src string) []Token {
	t.Helper()
	toks, err := Scan(src)
	if err != nil {
		t.Fatalf("scan %q: %v", src, err)
	}
	return toks
}

func kinds(toks []Token) []Kind {
	out := make([]Kind, len(toks))
	for i, tok := range toks {
		out[i] = tok.Kind
	}
	return out
}
