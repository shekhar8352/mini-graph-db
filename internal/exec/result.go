package exec

import (
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// DeprecatedSyntax is the notification code for a legacy statement that still runs.
const DeprecatedSyntax = "DeprecatedSyntax"

// Notification is one notice attached to a result.
type Notification struct {
	Code    string
	Message string
}

// Summary counts the writes of one statement.
// Setting a property counts even when the new value equals the old one.
// Adding a label the node already has does not count. Removing a missing
// label does not count. Indexes are counted when a statement stores or
// drops one.
type Summary struct {
	NodesCreated       int
	NodesDeleted       int
	EdgesCreated       int
	EdgesDeleted       int
	PropertiesSet      int
	LabelsAdded        int
	LabelsRemoved      int
	IndexesAdded       int
	IndexesRemoved     int
	ConstraintsAdded   int
	ConstraintsRemoved int
}

// Operator is one PROFILE line.
// Rows is the number of rows the operator yielded.
// DBHits counts node reads, edge reads, and adjacency-list reads charged to
// this operator. Time is the time spent in its Next calls, including its input.
type Operator struct {
	Text   string
	Depth  int
	Rows   int64
	DBHits int64
	Time   time.Duration
}

// Result is the outcome of the last statement in a script.
// Next walks the produced rows one at a time. The statement has already
// finished, including its commit or rollback.
type Result struct {
	Columns       []string
	Summary       Summary
	Notifications []Notification
	Profile       []Operator
	rows          [][]value.Value
	next          int
}

// Next returns the next row. The second result is false when the rows are exhausted.
func (r *Result) Next() ([]value.Value, bool) {
	if r == nil || r.next >= len(r.rows) {
		return nil, false
	}
	row := r.rows[r.next]
	r.next++
	return row, true
}

// All returns every row the statement produced.
func (r *Result) All() [][]value.Value {
	if r == nil {
		return nil
	}
	return r.rows
}
