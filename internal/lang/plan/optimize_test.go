package plan

import "testing"

func TestLimitPushdown(t *testing.T) {
	p, err := Build(`MATCH (a) RETURN a.name AS name LIMIT 3`)
	if err != nil {
		t.Fatal(err)
	}
	got := p.String()
	want := "Project(a.name AS name)\n  Limit(3)\n    NodeScan((a))\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
