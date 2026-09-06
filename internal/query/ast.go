package query

// Stmt is a parsed query-language statement.
type Stmt interface {
	stmt()
}

type CreateNodeStmt struct {
	Label string
	Props map[string]any
}

type CreateEdgeStmt struct {
	From  uint64
	To    uint64
	Label string
	Props map[string]any
}

type UpdateNodeStmt struct {
	ID    uint64
	Props map[string]any
}

type UpdateEdgeStmt struct {
	ID    uint64
	Props map[string]any
}

type MatchStmt struct {
	Label string
	Where *Where
}

type MatchEdgeStmt struct {
	Label string
	Where *Where
}

type EdgesStmt struct {
	From uint64
	To   uint64
}

type Where struct {
	Key   string
	Op    string
	Value any
}

type NeighborsStmt struct {
	ID    uint64
	Depth int
}

type PathStmt struct {
	From uint64
	To   uint64
}

type DeleteNodeStmt struct {
	ID uint64
}

type DeleteEdgeStmt struct {
	ID uint64
}

type GetNodeStmt struct {
	ID uint64
}

type GetEdgeStmt struct {
	ID uint64
}

type ShowStatsStmt struct{}
type HelpStmt struct{}
type ExitStmt struct{}

type SaveStmt struct {
	Path string
}

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
