package parser

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
)

func TestSpecExamples(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "docs", "spec", "examples")
	matches, err := filepath.Glob(filepath.Join(dir, "*.gql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 60 {
		t.Fatalf("corpus has %d scripts, want 60", len(matches))
	}
	for _, path := range matches {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			tree, err := Parse(string(src))
			if name == "e53-begin-read.gql" {
				if gerr.CodeOf(err) != gerr.Syntax {
					t.Fatalf("code %s err %v", gerr.CodeOf(err), err)
				}
				if !strings.Contains(err.Error(), "expected ONLY at ") {
					t.Fatalf("message %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			formatted := ast.Format(tree)
			again, err := Parse(formatted)
			if err != nil {
				t.Fatalf("reparse %q: %v", formatted, err)
			}
			if !reflect.DeepEqual(tree, again) {
				t.Fatalf("format\n%s", formatted)
			}
		})
	}
}
