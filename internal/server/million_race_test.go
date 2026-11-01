//go:build race

package server

import "testing"

func TestStreamMillionRows(t *testing.T) {
	t.Skip("streaming 1,000,000 rows is run without the race detector; make test runs it after the race suite")
}
