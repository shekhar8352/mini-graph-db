package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"

	graphdbv1 "github.com/shekhar8352/mini-graph-db/api/gen/graphdb/v1"
	"github.com/shekhar8352/mini-graph-db/internal/config"
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

func TestQueryAndRestart(t *testing.T) {
	dir := t.TempDir()
	srv := startServer(t, dir, func(cfg *config.Config) {
		cfg.Limits.MaxRowsPerMessage = 2
	})
	conn := dial(t, srv.Addr())
	ctx := context.Background()
	sum := execQuery(t, conn, ctx, `CREATE (:Person {name: "Ada"})`)
	if sum.GetNodesCreated() != 1 {
		t.Fatalf("created %d", sum.GetNodesCreated())
	}
	rows := execRows(t, conn, ctx, `MATCH (n:Person) RETURN n.name AS name`)
	if len(rows) != 1 || rows[0].GetValues()[0].GetStringValue() != "Ada" {
		t.Fatalf("rows %#v", rows)
	}
	shutdown(t, srv)

	srv = startServer(t, dir, nil)
	conn = dial(t, srv.Addr())
	rows = execRows(t, conn, ctx, `MATCH (n:Person) RETURN n.name AS name`)
	if len(rows) != 1 || rows[0].GetValues()[0].GetStringValue() != "Ada" {
		t.Fatalf("after restart %#v", rows)
	}
	st, err := graphdbv1.NewAdminServiceClient(conn).Stats(ctx, &graphdbv1.StatsRequest{Database: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if st.GetNodes() != 1 {
		t.Fatalf("stats %+v", st)
	}
	if _, err := execAdmin(t, conn).Checkpoint(ctx, &graphdbv1.CheckpointRequest{Database: "default"}); err != nil {
		t.Fatal(err)
	}
	vac, err := execAdmin(t, conn).Vacuum(ctx, &graphdbv1.VacuumRequest{Database: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if vac.GetReclaimed() < 0 {
		t.Fatalf("vacuum %d", vac.GetReclaimed())
	}
}

func TestTransactionsAcrossRPCs(t *testing.T) {
	srv := startServer(t, t.TempDir(), nil)
	conn := dial(t, srv.Addr())
	ctx := context.Background()
	tx := graphdbv1.NewTransactionServiceClient(conn)
	begun, err := tx.Begin(ctx, &graphdbv1.BeginRequest{ReadOnly: false})
	if err != nil {
		t.Fatal(err)
	}
	execQueryTxn(t, conn, ctx, begun.GetTxnId(), `CREATE (:Person {name: "Bea"})`)
	rows := execRowsTxn(t, conn, ctx, begun.GetTxnId(), `MATCH (n:Person) RETURN n.name AS name`)
	if len(rows) != 1 || rows[0].GetValues()[0].GetStringValue() != "Bea" {
		t.Fatalf("open txn %#v", rows)
	}
	if _, err := tx.Rollback(ctx, &graphdbv1.RollbackRequest{TxnId: begun.GetTxnId()}); err != nil {
		t.Fatal(err)
	}
	rows = execRows(t, conn, ctx, `MATCH (n:Person) RETURN n.name AS name`)
	if len(rows) != 0 {
		t.Fatalf("after rollback %#v", rows)
	}

	begun, err = tx.Begin(ctx, &graphdbv1.BeginRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Keepalive(ctx, &graphdbv1.KeepaliveRequest{TxnId: begun.GetTxnId()}); err != nil {
		t.Fatal(err)
	}
	execQueryTxn(t, conn, ctx, begun.GetTxnId(), `CREATE (:Person {name: "Cy"})`)
	if _, err := tx.Commit(ctx, &graphdbv1.CommitRequest{TxnId: begun.GetTxnId()}); err != nil {
		t.Fatal(err)
	}
	rows = execRows(t, conn, ctx, `MATCH (n:Person) RETURN n.name AS name`)
	if len(rows) != 1 || rows[0].GetValues()[0].GetStringValue() != "Cy" {
		t.Fatalf("committed %#v", rows)
	}
	listed, err := execAdmin(t, conn).ListTransactions(ctx, &graphdbv1.ListTransactionsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.GetTransactions()) != 0 {
		t.Fatalf("txns %+v", listed)
	}
}

func TestMultiDatabase(t *testing.T) {
	dir := t.TempDir()
	srv := startServer(t, dir, func(cfg *config.Config) {
		cfg.Storage.TrashRetention = time.Hour
	})
	conn := dial(t, srv.Addr())
	ctx := context.Background()
	admin := execAdmin(t, conn)
	if _, err := admin.CreateDatabase(ctx, &graphdbv1.CreateDatabaseRequest{Name: "analytics"}); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.CreateDatabase(ctx, &graphdbv1.CreateDatabaseRequest{Name: "analytics"}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("dup %v", err)
	}
	execQuery(t, conn, ctx, `USE analytics`)
	execQueryDB(t, conn, ctx, "", `CREATE (:Report {id: 1})`)
	rows := execRowsDB(t, conn, ctx, "default", `MATCH (n:Report) RETURN n.id AS id`)
	if len(rows) != 0 {
		t.Fatalf("default saw analytics %#v", rows)
	}
	rows = execRowsDB(t, conn, ctx, "analytics", `MATCH (n:Report) RETURN n.id AS id`)
	if len(rows) != 1 {
		t.Fatalf("analytics %#v", rows)
	}
	names := showNames(t, conn, ctx, `SHOW DATABASES`)
	if !has(names, "analytics") || !has(names, "default") || !has(names, "system") {
		t.Fatalf("names %v", names)
	}
	if _, err := admin.DropDatabase(ctx, &graphdbv1.DropDatabaseRequest{Name: "analytics", Confirm: false}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("drop without confirm %v", err)
	}
	if _, err := admin.DropDatabase(ctx, &graphdbv1.DropDatabaseRequest{Name: "analytics", Confirm: true}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, ".trash"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("trash %v %v", entries, err)
	}
	if _, err := admin.DropDatabase(ctx, &graphdbv1.DropDatabaseRequest{Name: "default", Confirm: true}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("drop default %v", err)
	}
}

func TestStreamingCancelAndErrors(t *testing.T) {
	srv := startServer(t, t.TempDir(), func(cfg *config.Config) {
		cfg.Limits.MaxRowsPerMessage = 10
		cfg.Limits.QueryTimeout = time.Minute
	})
	conn := dial(t, srv.Addr())
	ctx := context.Background()
	rows := execRows(t, conn, ctx, `UNWIND range(1, 25) AS n RETURN n`)
	if len(rows) != 25 {
		t.Fatalf("rows %d", len(rows))
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	stream, err := graphdbv1.NewQueryServiceClient(conn).Execute(canceled, &graphdbv1.ExecuteRequest{
		Query: `UNWIND range(1, 1000000) AS n RETURN n`,
	})
	if err == nil {
		_, err = stream.Recv()
	}
	if status.Code(err) != codes.Canceled && status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("cancel %v", err)
	}

	stream, err = graphdbv1.NewQueryServiceClient(conn).Execute(ctx, &graphdbv1.ExecuteRequest{Query: `MATCH`})
	if err != nil {
		t.Fatal(err)
	}
	_, err = stream.Recv()
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("syntax %v", err)
	}
	var info *graphdbv1.ErrorInfo
	for _, d := range st.Details() {
		if v, ok := d.(*graphdbv1.ErrorInfo); ok {
			info = v
		}
	}
	if info == nil || info.GetCode() != string(gerr.Syntax) || info.GetPosition().GetLine() < 1 {
		t.Fatalf("info %+v", info)
	}
}

func TestStreamMillionRows(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	srv := startServer(t, t.TempDir(), func(cfg *config.Config) {
		cfg.Limits.MaxRowsPerMessage = 1024
		cfg.Limits.QueryTimeout = 3 * time.Minute
		cfg.Limits.QueryMemoryBytes = 64 << 20
	})
	conn := dial(t, srv.Addr())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	n := countRows(t, conn, ctx, `UNWIND range(1, 1000000) AS n RETURN n`)
	if n != 1_000_000 {
		t.Fatalf("rows %d", n)
	}
}

func countRows(t *testing.T, conn *grpc.ClientConn, ctx context.Context, query string) int {
	t.Helper()
	stream, err := graphdbv1.NewQueryServiceClient(conn).Execute(ctx, &graphdbv1.ExecuteRequest{Query: query})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return n
		}
		if err != nil {
			t.Fatal(err)
		}
		if msg.GetRows() != nil {
			n += len(msg.GetRows().GetRows())
		}
	}
}

func TestConnectionLimit(t *testing.T) {
	srv := startServer(t, t.TempDir(), func(cfg *config.Config) {
		cfg.Server.MaxConnections = 2
	})
	c1 := dial(t, srv.Addr())
	c2 := dial(t, srv.Addr())
	c3 := dial(t, srv.Addr())
	ctx := context.Background()
	execQuery(t, c1, ctx, `RETURN 1 AS n`)
	execQuery(t, c2, ctx, `RETURN 1 AS n`)
	stream, err := graphdbv1.NewQueryServiceClient(c3).Execute(ctx, &graphdbv1.ExecuteRequest{Query: `RETURN 1 AS n`})
	if err != nil {
		t.Fatal(err)
	}
	_, err = stream.Recv()
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("third connection %v", err)
	}
}

func TestIdleTransactionAndDisconnect(t *testing.T) {
	srv := startServer(t, t.TempDir(), func(cfg *config.Config) {
		cfg.Limits.TxnIdle = 200 * time.Millisecond
	})
	conn := dial(t, srv.Addr())
	ctx := context.Background()
	tx := graphdbv1.NewTransactionServiceClient(conn)
	begun, err := tx.Begin(ctx, &graphdbv1.BeginRequest{})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond)
	_, err = tx.Commit(ctx, &graphdbv1.CommitRequest{TxnId: begun.GetTxnId()})
	if status.Code(err) != codes.NotFound && status.Code(err) != codes.InvalidArgument {
		t.Fatalf("idle commit %v", err)
	}

	begun, err = tx.Begin(ctx, &graphdbv1.BeginRequest{})
	if err != nil {
		t.Fatal(err)
	}
	execQueryTxn(t, conn, ctx, begun.GetTxnId(), `CREATE (:Person {name: "Temp"})`)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	conn = dial(t, srv.Addr())
	rows := execRows(t, conn, ctx, `MATCH (n:Person) RETURN n.name AS name`)
	if len(rows) != 0 {
		t.Fatalf("disconnect leaked %#v", rows)
	}
	listed, err := execAdmin(t, conn).ListTransactions(ctx, &graphdbv1.ListTransactionsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.GetTransactions()) != 0 {
		t.Fatalf("leftover %+v", listed)
	}
}

func TestServicesAndBatch(t *testing.T) {
	srv := startServer(t, t.TempDir(), nil)
	conn := dial(t, srv.Addr())
	ctx := context.Background()
	health, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil || health.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("health %v %+v", err, health)
	}
	ref, err := grpc_reflection_v1.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := ref.Send(&grpc_reflection_v1.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1.ServerReflectionRequest_ListServices{},
	}); err != nil {
		t.Fatal(err)
	}
	resp, err := ref.Recv()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, svc := range resp.GetListServicesResponse().GetService() {
		got[svc.GetName()] = true
	}
	for _, name := range []string{
		"graphdb.v1.QueryService",
		"graphdb.v1.TransactionService",
		"graphdb.v1.AdminService",
		"grpc.health.v1.Health",
		"grpc.reflection.v1.ServerReflection",
	} {
		if !got[name] {
			t.Fatalf("missing %s in %v", name, got)
		}
	}
	stream, err := graphdbv1.NewQueryServiceClient(conn).ExecuteBatch(ctx, &graphdbv1.ExecuteBatchRequest{
		Queries: []string{`CREATE (:Person {name: "A"})`, `MATCH (n:Person) RETURN n.name AS name`},
	})
	if err != nil {
		t.Fatal(err)
	}
	var rows int
	var created int64
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if msg.GetRows() != nil {
			rows += len(msg.GetRows().GetRows())
		}
		if msg.GetSummary() != nil {
			created += msg.GetSummary().GetNodesCreated()
		}
	}
	if created != 1 || rows != 1 {
		t.Fatalf("batch created %d rows %d", created, rows)
	}
}

func TestPanicBecomesInternal(t *testing.T) {
	h := &Host{}
	_, err := h.unary(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/test/Panic"}, func(context.Context, any) (any, error) {
		panic("boom")
	})
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Internal {
		t.Fatalf("panic %v", err)
	}
	if h.metrics.Panics.Load() != 1 {
		t.Fatalf("panics %d", h.metrics.Panics.Load())
	}
}

func TestDevCert(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	key := filepath.Join(dir, "key.pem")
	if err := WriteDevCert(cert, key, nil); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Storage.DataDir = filepath.Join(dir, "data")
	cfg.Server.Bind = "127.0.0.1:0"
	cfg.Server.PIDFile = filepath.Join(dir, "pid")
	cfg.Server.TLS.Cert = cert
	cfg.Server.TLS.Key = key
	srv, err := Listen(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, srv)
	go func() { _ = srv.Serve() }()
	creds := credentialsFrom(t, cert)
	conn, err := grpc.NewClient("passthrough:///"+srv.Addr(), grpc.WithTransportCredentials(creds))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	execQuery(t, conn, context.Background(), `RETURN 1 AS n`)
}

func startServer(t *testing.T, dir string, tweak func(*config.Config)) *Server {
	t.Helper()
	cfg := config.Defaults()
	cfg.Storage.DataDir = dir
	cfg.Server.Bind = "127.0.0.1:0"
	cfg.Server.PIDFile = filepath.Join(dir, "graphdb.pid")
	if tweak != nil {
		tweak(&cfg)
	}
	srv, err := Listen(cfg)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { shutdown(t, srv) })
	return srv
}

func shutdown(t *testing.T, srv *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func dial(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///"+addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func execAdmin(t *testing.T, conn *grpc.ClientConn) graphdbv1.AdminServiceClient {
	t.Helper()
	return graphdbv1.NewAdminServiceClient(conn)
}

func credentialsFrom(t *testing.T, certPath string) credentials.TransportCredentials {
	t.Helper()
	raw, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		t.Fatal("no cert")
	}
	return credentials.NewTLS(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12})
}

func execQuery(t *testing.T, conn *grpc.ClientConn, ctx context.Context, query string) *graphdbv1.Summary {
	t.Helper()
	return execQueryDB(t, conn, ctx, "", query)
}

func execQueryDB(t *testing.T, conn *grpc.ClientConn, ctx context.Context, db, query string) *graphdbv1.Summary {
	t.Helper()
	_, sum := collect(t, conn, ctx, db, "", query)
	return sum
}

func execQueryTxn(t *testing.T, conn *grpc.ClientConn, ctx context.Context, txn, query string) *graphdbv1.Summary {
	t.Helper()
	_, sum := collect(t, conn, ctx, "", txn, query)
	return sum
}

func execRows(t *testing.T, conn *grpc.ClientConn, ctx context.Context, query string) []*graphdbv1.Row {
	t.Helper()
	rows, _ := collect(t, conn, ctx, "", "", query)
	return rows
}

func execRowsDB(t *testing.T, conn *grpc.ClientConn, ctx context.Context, db, query string) []*graphdbv1.Row {
	t.Helper()
	rows, _ := collect(t, conn, ctx, db, "", query)
	return rows
}

func execRowsTxn(t *testing.T, conn *grpc.ClientConn, ctx context.Context, txn, query string) []*graphdbv1.Row {
	t.Helper()
	rows, _ := collect(t, conn, ctx, "", txn, query)
	return rows
}

func showNames(t *testing.T, conn *grpc.ClientConn, ctx context.Context, query string) []string {
	t.Helper()
	rows := execRows(t, conn, ctx, query)
	var names []string
	for _, row := range rows {
		names = append(names, row.GetValues()[0].GetStringValue())
	}
	return names
}

func collect(t *testing.T, conn *grpc.ClientConn, ctx context.Context, db, txn, query string) ([]*graphdbv1.Row, *graphdbv1.Summary) {
	t.Helper()
	stream, err := graphdbv1.NewQueryServiceClient(conn).Execute(ctx, &graphdbv1.ExecuteRequest{
		Database: db,
		TxnId:    txn,
		Query:    query,
	})
	if err != nil {
		t.Fatal(err)
	}
	var rows []*graphdbv1.Row
	var sum *graphdbv1.Summary
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if msg.GetRows() != nil {
			rows = append(rows, msg.GetRows().GetRows()...)
		}
		if msg.GetSummary() != nil {
			sum = msg.GetSummary()
		}
	}
	return rows, sum
}

func has(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
