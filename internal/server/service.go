package server

import (
	"context"
	"sort"
	"time"

	"google.golang.org/grpc"

	graphdbv1 "github.com/shekhar8352/mini-graph-db/api/gen/graphdb/v1"
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

type queryServer struct {
	graphdbv1.UnimplementedQueryServiceServer
	host *Host
}

func (q *queryServer) Execute(req *graphdbv1.ExecuteRequest, stream grpc.ServerStreamingServer[graphdbv1.ExecuteResponse]) error {
	return q.run(req.GetDatabase(), req.GetTxnId(), []string{req.GetQuery()}, req.GetParams(), req.GetTimeoutMs(), stream)
}

func (q *queryServer) ExecuteBatch(req *graphdbv1.ExecuteBatchRequest, stream grpc.ServerStreamingServer[graphdbv1.ExecuteResponse]) error {
	return q.run(req.GetDatabase(), req.GetTxnId(), req.GetQueries(), req.GetParams(), req.GetTimeoutMs(), stream)
}

func (q *queryServer) run(database, txnID string, queries []string, params map[string]*graphdbv1.Value, timeoutMS int64, stream grpc.ServerStreamingServer[graphdbv1.ExecuteResponse]) error {
	sess, err := q.host.sessionFrom(stream.Context())
	if err != nil {
		return err
	}
	parsed, err := fromParams(params)
	if err != nil {
		return err
	}
	if timeoutMS < 0 {
		return gerr.New(gerr.InvalidArgument, "timeout_ms is negative")
	}
	var timeout time.Duration
	if timeoutMS > 0 {
		timeout = time.Duration(timeoutMS) * time.Millisecond
	}
	return sess.Run(stream.Context(), database, txnID, queries, parsed, timeout, stream.Send)
}

type txnServer struct {
	graphdbv1.UnimplementedTransactionServiceServer
	host *Host
}

func (t *txnServer) Begin(ctx context.Context, req *graphdbv1.BeginRequest) (*graphdbv1.BeginResponse, error) {
	sess, err := t.host.sessionFrom(ctx)
	if err != nil {
		return nil, err
	}
	id, db, err := sess.Begin(req.GetDatabase(), req.GetReadOnly())
	if err != nil {
		return nil, err
	}
	return &graphdbv1.BeginResponse{TxnId: id, Database: db}, nil
}

func (t *txnServer) Commit(ctx context.Context, req *graphdbv1.CommitRequest) (*graphdbv1.CommitResponse, error) {
	sess, err := t.host.sessionFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err := sess.Commit(req.GetTxnId()); err != nil {
		return nil, err
	}
	return &graphdbv1.CommitResponse{}, nil
}

func (t *txnServer) Rollback(ctx context.Context, req *graphdbv1.RollbackRequest) (*graphdbv1.RollbackResponse, error) {
	sess, err := t.host.sessionFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err := sess.Rollback(req.GetTxnId()); err != nil {
		return nil, err
	}
	return &graphdbv1.RollbackResponse{}, nil
}

func (t *txnServer) Keepalive(ctx context.Context, req *graphdbv1.KeepaliveRequest) (*graphdbv1.KeepaliveResponse, error) {
	sess, err := t.host.sessionFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err := sess.Keepalive(req.GetTxnId()); err != nil {
		return nil, err
	}
	return &graphdbv1.KeepaliveResponse{}, nil
}

type adminServer struct {
	graphdbv1.UnimplementedAdminServiceServer
	host *Host
}

func (a *adminServer) CreateDatabase(_ context.Context, req *graphdbv1.CreateDatabaseRequest) (*graphdbv1.CreateDatabaseResponse, error) {
	if err := a.host.CreateDatabase(req.GetName()); err != nil {
		return nil, err
	}
	return &graphdbv1.CreateDatabaseResponse{}, nil
}

func (a *adminServer) DropDatabase(_ context.Context, req *graphdbv1.DropDatabaseRequest) (*graphdbv1.DropDatabaseResponse, error) {
	if err := a.host.DropDatabase(req.GetName(), req.GetConfirm()); err != nil {
		return nil, err
	}
	return &graphdbv1.DropDatabaseResponse{}, nil
}

func (a *adminServer) ListDatabases(context.Context, *graphdbv1.ListDatabasesRequest) (*graphdbv1.ListDatabasesResponse, error) {
	list := a.host.ListDatabases()
	out := &graphdbv1.ListDatabasesResponse{Databases: make([]*graphdbv1.Database, len(list))}
	for i, db := range list {
		out.Databases[i] = &graphdbv1.Database{Name: db.name, CreatedUnixNano: db.created}
	}
	return out, nil
}

func (a *adminServer) ListTransactions(context.Context, *graphdbv1.ListTransactionsRequest) (*graphdbv1.ListTransactionsResponse, error) {
	list := a.host.reg.list()
	sort.Slice(list, func(i, j int) bool { return list[i].id < list[j].id })
	out := &graphdbv1.ListTransactionsResponse{Transactions: make([]*graphdbv1.TransactionInfo, len(list))}
	now := time.Now()
	for i, tx := range list {
		out.Transactions[i] = &graphdbv1.TransactionInfo{
			Id:       tx.idStr(),
			Database: tx.db,
			ReadOnly: tx.readOnly,
			AgeNanos: now.Sub(tx.started).Nanoseconds(),
		}
	}
	return out, nil
}

func (a *adminServer) TerminateTransaction(_ context.Context, req *graphdbv1.TerminateTransactionRequest) (*graphdbv1.TerminateTransactionResponse, error) {
	id, err := parseTxnID(req.GetId())
	if err != nil {
		return nil, err
	}
	if err := a.host.reg.terminate(id); err != nil {
		return nil, err
	}
	return &graphdbv1.TerminateTransactionResponse{}, nil
}

func (a *adminServer) Checkpoint(_ context.Context, req *graphdbv1.CheckpointRequest) (*graphdbv1.CheckpointResponse, error) {
	if err := a.host.Checkpoint(req.GetDatabase()); err != nil {
		return nil, err
	}
	return &graphdbv1.CheckpointResponse{}, nil
}

func (a *adminServer) Vacuum(ctx context.Context, req *graphdbv1.VacuumRequest) (*graphdbv1.VacuumResponse, error) {
	n, err := a.host.Vacuum(ctx, req.GetDatabase())
	if err != nil {
		return nil, err
	}
	return &graphdbv1.VacuumResponse{Reclaimed: int64(n)}, nil
}

func (a *adminServer) Stats(_ context.Context, req *graphdbv1.StatsRequest) (*graphdbv1.StatsResponse, error) {
	st, err := a.host.Stats(req.GetDatabase())
	if err != nil {
		return nil, err
	}
	return &graphdbv1.StatsResponse{
		Database:          st.name,
		Nodes:             st.nodes,
		Edges:             st.edges,
		Labels:            st.labels,
		Keys:              st.keys,
		Commits:           st.commits,
		Rollbacks:         st.rollbacks,
		VersionsReclaimed: st.reclaimed,
	}, nil
}
