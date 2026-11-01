//go:build !race

package server

import (
	"context"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc"

	graphdbv1 "github.com/shekhar8352/mini-graph-db/api/gen/graphdb/v1"
	"github.com/shekhar8352/mini-graph-db/internal/config"
)

func TestStreamMillionRows(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	srv := startServer(t, t.TempDir(), func(cfg *config.Config) {
		cfg.Limits.MaxRowsPerMessage = 1024
		cfg.Limits.QueryTimeout = 2 * time.Minute
		cfg.Limits.QueryMemoryBytes = 64 << 20
	})
	conn := dial(t, srv.Addr())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	n := countRows(ctx, t, conn, `UNWIND range(1, 1000000) AS n RETURN n`)
	if n != 1_000_000 {
		t.Fatalf("rows %d", n)
	}
}

func countRows(ctx context.Context, t *testing.T, conn *grpc.ClientConn, query string) int {
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
