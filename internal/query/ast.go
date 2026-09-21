package query

// Stmt is a parsed query-language statement.
type Stmt interface {
	stmt()
}

// CreateNodeStmt is CREATE NODE <label> {props}.
type CreateNodeStmt struct {
	Label string
	Props map[string]any
}

// CreateEdgeStmt is CREATE EDGE <from> -<LABEL>-> <to> {props}.
type CreateEdgeStmt struct {
	From  uint64
	To    uint64
	Label string
	Props map[string]any
}

// UpdateNodeStmt is UPDATE NODE <id> {props}.
type UpdateNodeStmt struct {
	ID    uint64
	Props map[string]any
}

// UpdateEdgeStmt is UPDATE EDGE <id> {props}.
type UpdateEdgeStmt struct {
	ID    uint64
	Props map[string]any
}

// MatchStmt is MATCH <label> [WHERE ...].
type MatchStmt struct {
	Label string
	Where *Where
}

// MatchEdgeStmt is MATCH EDGE <label> [WHERE ...].
type MatchEdgeStmt struct {
	Label string
	Where *Where
}

// EdgesStmt is EDGES <from> TO <to>.
type EdgesStmt struct {
	From uint64
	To   uint64
}

// Where is a single property predicate.
type Where struct {
	Key   string
	Op    string
	Value any
}

// NeighborsStmt is NEIGHBORS <id> [DEPTH n].
type NeighborsStmt struct {
	ID    uint64
	Depth int
}

// PathStmt is PATH <from> TO <to>.
type PathStmt struct {
	From uint64
	To   uint64
}

// DeleteNodeStmt is DELETE NODE <id>.
type DeleteNodeStmt struct {
	ID uint64
}

// DeleteEdgeStmt is DELETE EDGE <id>.
type DeleteEdgeStmt struct {
	ID uint64
}

// GetNodeStmt is GET NODE <id>.
type GetNodeStmt struct {
	ID uint64
}

// GetEdgeStmt is GET EDGE <id>.
type GetEdgeStmt struct {
	ID uint64
}

// ShowStatsStmt is SHOW STATS.
type ShowStatsStmt struct{}

// HelpStmt is HELP.
type HelpStmt struct{}

// ExitStmt is EXIT or QUIT.
type ExitStmt struct{}

// SaveStmt is SAVE <file>.
type SaveStmt struct {
	Path string
}

// LoadStmt is LOAD <file>.
type LoadStmt struct {
	Path string
}

func (CreateNodeStmt) stmt() {}
func (CreateEdgeStmt) stmt() {}
func (UpdateNodeStmt) stmt() {}
func (UpdateEdgeStmt) stmt() {}
func (MatchStmt) stmt()      {}
func (MatchEdgeStmt) stmt()  {}
func (EdgesStmt) stmt()      {}
func (NeighborsStmt) stmt()  {}
func (PathStmt) stmt()       {}
func (DeleteNodeStmt) stmt() {}
func (DeleteEdgeStmt) stmt() {}
func (GetNodeStmt) stmt()    {}
func (GetEdgeStmt) stmt()    {}
func (ShowStatsStmt) stmt()  {}
func (HelpStmt) stmt()       {}
func (ExitStmt) stmt()       {}
func (SaveStmt) stmt()       {}
func (LoadStmt) stmt()       {}
