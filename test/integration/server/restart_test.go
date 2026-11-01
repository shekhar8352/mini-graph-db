package server_test

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	graphdbv1 "github.com/shekhar8352/mini-graph-db/api/gen/graphdb/v1"
)

// TestKill9Restart commits through the real server process, kills it with
// SIGKILL, and checks that every acknowledged commit is still there.
func TestKill9Restart(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	root := moduleRoot(t)
	bin := filepath.Join(t.TempDir(), "graphdb")
	build := exec.Command("go", "build", "-o", bin, "./cmd/graphdb")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	dir := t.TempDir()
	proc, addr := startServer(t, bin, dir)
	conn := dial(t, addr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var names []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		q := graphdbv1.NewQueryServiceClient(conn)
		for i := 1; i <= 40; i++ {
			name := "n" + itoa(i)
			cctx, stop := context.WithTimeout(ctx, 5*time.Second)
			err := query(cctx, q, `CREATE (:Person {name: "`+name+`"})`)
			stop()
			if err != nil {
				return
			}
			mu.Lock()
			names = append(names, name)
			mu.Unlock()
		}
	}()
	deadline := time.Now().Add(30 * time.Second)
	for {
		mu.Lock()
		n := len(names)
		mu.Unlock()
		if n >= 10 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := proc.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = proc.Wait()
	<-done
	mu.Lock()
	acked := append([]string(nil), names...)
	mu.Unlock()
	if len(acked) < 10 {
		t.Fatalf("acknowledged %d", len(acked))
	}
	_ = conn.Close()

	proc, addr = startServer(t, bin, dir)
	defer func() {
		_ = proc.Process.Signal(os.Interrupt)
		doneWait := make(chan struct{})
		go func() {
			_ = proc.Wait()
			close(doneWait)
		}()
		select {
		case <-doneWait:
		case <-time.After(10 * time.Second):
			_ = proc.Process.Kill()
		}
	}()
	conn = dial(t, addr)
	defer func() { _ = conn.Close() }()
	got := map[string]bool{}
	for _, row := range rows(t, conn, `MATCH (n:Person) RETURN n.name AS name`) {
		got[row.GetValues()[0].GetStringValue()] = true
	}
	for _, name := range acked {
		if !got[name] {
			t.Fatalf("missing acknowledged %s (%d present)", name, len(got))
		}
	}
}

func startServer(t *testing.T, bin, dir string) (*exec.Cmd, string) {
	t.Helper()
	cmd := exec.Command(bin, "server", "--data-dir", dir, "--bind", "127.0.0.1:0")
	cmd.Env = withoutGraphDBEnv()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line := make(chan string, 1)
	go func() {
		r := bufio.NewReader(stdout)
		text, err := r.ReadString('\n')
		if err != nil {
			line <- ""
			return
		}
		line <- strings.TrimSpace(text)
		_, _ = io.Copy(io.Discard, r)
	}()
	select {
	case text := <-line:
		if !strings.HasPrefix(text, "listening ") {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("banner %q", text)
		}
		return cmd, strings.TrimPrefix(text, "listening ")
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("server did not listen")
	}
	return nil, ""
}

func query(ctx context.Context, q graphdbv1.QueryServiceClient, text string) error {
	stream, err := q.Execute(ctx, &graphdbv1.ExecuteRequest{Query: text})
	if err != nil {
		return err
	}
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func rows(t *testing.T, conn *grpc.ClientConn, text string) []*graphdbv1.Row {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stream, err := graphdbv1.NewQueryServiceClient(conn).Execute(ctx, &graphdbv1.ExecuteRequest{Query: text})
	if err != nil {
		t.Fatal(err)
	}
	var out []*graphdbv1.Row
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		if msg.GetRows() != nil {
			out = append(out, msg.GetRows().GetRows()...)
		}
	}
}

func dial(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///"+addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func withoutGraphDBEnv() []string {
	var out []string
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "GRAPHDB_") {
			continue
		}
		out = append(out, env)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
