package server

import (
	"context"
	"strconv"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/exec"
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/lang/parser"
	"github.com/shekhar8352/mini-graph-db/internal/lang/sema"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// Run executes queries on this connection and streams each statement result.
// The caller holds no locks. Run holds the session lock for the call.
func (s *Session) Run(ctx context.Context, database, txnID string, queries []string, params map[string]value.Value, timeout time.Duration, send sender) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkTxn(database, txnID); err != nil {
		return err
	}
	limit := s.host.cfg.Limits.QueryTimeout
	if timeout > 0 && timeout < limit {
		limit = timeout
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	idx := int32(0)
	for _, q := range queries {
		n, err := s.runScript(ctx, database, q, params, idx, send)
		if err != nil {
			return err
		}
		idx += n
	}
	if len(queries) == 0 {
		return sendResult(send, 0, exec.Rows(nil, nil), s.txnID(), s.host.cfg.Limits.MaxRowsPerMessage, s.host.cfg.Limits.MaxMessageBytes)
	}
	return nil
}

func (s *Session) checkTxn(database, txnID string) error {
	if s.tx != nil {
		s.tx.touch()
	}
	if txnID == "" {
		if s.tx != nil && database != "" && database != s.tx.db {
			return gerr.New(gerr.InvalidArgument, "database does not match the open transaction")
		}
		return nil
	}
	id, err := parseTxnID(txnID)
	if err != nil {
		return err
	}
	if s.tx == nil || s.tx.id != id {
		return gerr.Newf(gerr.NotFound, "transaction %s", txnID)
	}
	if database != "" && database != s.tx.db {
		return gerr.New(gerr.InvalidArgument, "database does not match the open transaction")
	}
	return nil
}

func (s *Session) runScript(ctx context.Context, database, src string, params map[string]value.Value, idx int32, send sender) (int32, error) {
	if err := sema.Check(src); err != nil {
		return 0, err
	}
	script, err := parser.Parse(src)
	if err != nil {
		return 0, err
	}
	var stmts []ast.Stmt
	if script != nil {
		stmts = script.Stmts
	}
	if len(stmts) == 0 {
		if err := sendResult(send, idx, exec.Rows(nil, nil), s.txnID(), s.host.cfg.Limits.MaxRowsPerMessage, s.host.cfg.Limits.MaxMessageBytes); err != nil {
			return 0, err
		}
		return 1, nil
	}
	current := s.db
	if s.tx != nil {
		current = s.tx.db
	} else if database != "" {
		current = database
	}
	for i, st := range stmts {
		if err := ctx.Err(); err != nil {
			return int32(i), err
		}
		res, err := s.runStmt(ctx, &current, st, params)
		if err != nil {
			return int32(i), err
		}
		if err := sendResult(send, idx+int32(i), res, s.txnID(), s.host.cfg.Limits.MaxRowsPerMessage, s.host.cfg.Limits.MaxMessageBytes); err != nil {
			return int32(i), err
		}
	}
	return int32(len(stmts)), nil
}

func (s *Session) runStmt(ctx context.Context, current *string, st ast.Stmt, params map[string]value.Value) (*exec.Result, error) {
	switch n := st.(type) {
	case *ast.Use:
		return s.use(n.Name, current)
	case *ast.CreateDatabase:
		if s.tx != nil {
			return nil, gerr.New(gerr.InvalidArgument, "cannot create a database inside a transaction")
		}
		if err := s.host.CreateDatabase(n.Name); err != nil {
			return nil, err
		}
		return exec.Rows(nil, nil), nil
	case *ast.DropDatabase:
		if s.tx != nil {
			return nil, gerr.New(gerr.InvalidArgument, "cannot drop a database inside a transaction")
		}
		if err := s.host.DropDatabase(n.Name, n.Confirm); err != nil {
			return nil, err
		}
		if s.db == n.Name {
			s.db = "default"
			*current = s.db
		}
		return exec.Rows(nil, nil), nil
	case *ast.Show:
		if n.Kind == ast.ShowDatabases {
			return s.showDatabases(), nil
		}
		if n.Kind == ast.ShowTransactions {
			return s.showTransactions(), nil
		}
	case *ast.Terminate:
		if err := s.terminate(n.ID); err != nil {
			return nil, err
		}
		return exec.Rows(nil, nil), nil
	}
	return s.execOn(ctx, *current, ast.Format(st), params)
}

func (s *Session) use(name string, current *string) (*exec.Result, error) {
	if !s.host.hasDB(name) {
		return nil, gerr.Newf(gerr.NotFound, "database %s", name)
	}
	if s.tx != nil && name != s.tx.db {
		return nil, gerr.New(gerr.InvalidArgument, "cannot change database inside a transaction")
	}
	s.db = name
	*current = name
	return exec.Rows(nil, nil), nil
}

func (s *Session) showDatabases() *exec.Result {
	list := s.host.listDatabases()
	rows := make([][]value.Value, len(list))
	for i, db := range list {
		rows[i] = []value.Value{value.String(db.name)}
	}
	return exec.Rows([]string{"name"}, rows)
}

func (s *Session) showTransactions() *exec.Result {
	list := s.host.reg.list()
	rows := make([][]value.Value, len(list))
	now := time.Now()
	for i, tx := range list {
		age := now.Sub(tx.started)
		rows[i] = []value.Value{
			value.Int(tx.id),
			value.Bool(tx.readOnly),
			value.Duration(0, 0, age.Nanoseconds()),
		}
	}
	return exec.Rows([]string{"id", "readOnly", "age"}, rows)
}

func (s *Session) terminate(id int64) error {
	if s.tx != nil && s.tx.id == id {
		return s.finishRollback()
	}
	s.mu.Unlock()
	err := s.host.reg.terminate(id)
	s.mu.Lock()
	return err
}

func (s *Session) execOn(ctx context.Context, dbName, src string, params map[string]value.Value) (*exec.Result, error) {
	opt := exec.Options{Params: params, Budget: s.host.cfg.Limits.QueryMemoryBytes}
	if s.tx != nil {
		if dbName != s.tx.db {
			return nil, gerr.New(gerr.InvalidArgument, "database does not match the open transaction")
		}
		s.tx.touch()
		res, err := exec.Run(ctx, s.tx.g, src, opt)
		if err != nil {
			return nil, err
		}
		if !s.tx.g.InTransaction() {
			s.host.release(s.tx.dbRef)
			s.host.reg.remove(s.tx.id)
			s.tx = nil
		}
		return res, nil
	}
	db, err := s.host.acquire(dbName)
	if err != nil {
		return nil, err
	}
	g := s.host.graph(db)
	res, err := exec.Run(ctx, g, src, opt)
	if err != nil {
		if g.InTransaction() {
			_ = g.Rollback()
		}
		s.host.release(db)
		return nil, err
	}
	if g.InTransaction() {
		if err := s.adopt(db, g); err != nil {
			_ = g.Rollback()
			s.host.release(db)
			return nil, err
		}
		return res, nil
	}
	s.host.release(db)
	return res, nil
}

// Begin opens a transaction on database, or on the session database when empty.
func (s *Session) Begin(database string, readOnly bool) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tx != nil {
		return "", "", gerr.New(gerr.InvalidArgument, "transaction already open")
	}
	name := s.db
	if database != "" {
		name = database
	}
	db, err := s.host.acquire(name)
	if err != nil {
		return "", "", err
	}
	g := s.host.graph(db)
	if err := g.Begin(readOnly); err != nil {
		s.host.release(db)
		return "", "", err
	}
	if err := s.adopt(db, g); err != nil {
		_ = g.Rollback()
		s.host.release(db)
		return "", "", err
	}
	return s.tx.idStr(), name, nil
}

// Commit commits txnID when it belongs to this connection.
func (s *Session) Commit(txnID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.own(txnID); err != nil {
		return err
	}
	return s.finishCommit()
}

// Rollback aborts txnID when it belongs to this connection.
func (s *Session) Rollback(txnID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.own(txnID); err != nil {
		return err
	}
	return s.finishRollback()
}

// Keepalive refreshes the idle timer for txnID.
func (s *Session) Keepalive(txnID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.own(txnID); err != nil {
		return err
	}
	s.tx.touch()
	return nil
}

func (s *Session) own(txnID string) error {
	id, err := parseTxnID(txnID)
	if err != nil {
		return err
	}
	if s.tx == nil || s.tx.id != id {
		return gerr.Newf(gerr.NotFound, "transaction %s", txnID)
	}
	return nil
}

func (s *Session) txnID() string {
	if s.tx == nil {
		return ""
	}
	return s.tx.idStr()
}

func parseTxnID(s string) (int64, error) {
	if s == "" {
		return 0, gerr.New(gerr.InvalidArgument, "transaction id is empty")
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, gerr.Newf(gerr.InvalidArgument, "transaction id %s", s)
	}
	return id, nil
}
