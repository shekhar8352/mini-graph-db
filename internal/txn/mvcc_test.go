package txn

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/enginetest"
	"github.com/shekhar8352/mini-graph-db/internal/storage/graphstore"
	"github.com/shekhar8352/mini-graph-db/internal/storage/memory"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func TestConformance(t *testing.T) {
	enginetest.Run(t, func() storage.Engine {
		m, err := Open(memory.Open(), Options{})
		if err != nil {
			t.Fatal(err)
		}
		return m
	})
}

func TestVisible(t *testing.T) {
	if !Visible(1, 0, 1) || !Visible(1, 4, 3) {
		t.Fatal("live or not-yet-deleted version should be visible")
	}
	if Visible(2, 0, 1) || Visible(1, 2, 2) || Visible(1, 2, 5) {
		t.Fatal("future or deleted version should be hidden")
	}
}

func TestSnapshotHidesLaterWrites(t *testing.T) {
	m := openMgr(t, Options{})
	putKey(t, m, "a", "1")

	r, err := m.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	putKey(t, m, "a", "2")
	got, err := r.Get(storage.KSNode, []byte("a"))
	if err != nil || string(got) != "1" {
		t.Fatalf("reader saw %q %v", got, err)
	}
	if err := r.Rollback(); err != nil {
		t.Fatal(err)
	}
	got = getKey(t, m, "a")
	if string(got) != "2" {
		t.Fatalf("latest: %q", got)
	}
}

func TestDeleteHiddenFromNewSnapshot(t *testing.T) {
	m := openMgr(t, Options{})
	putKey(t, m, "a", "1")
	r, err := m.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := m.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(storage.KSNode, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(storage.KSNode, []byte("a"))
	if err != nil || string(got) != "1" {
		t.Fatalf("old reader: %q %v", got, err)
	}
	if err := r.Rollback(); err != nil {
		t.Fatal(err)
	}
	tx, err = m.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Get(storage.KSNode, []byte("a")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deleted key: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestLostUpdate(t *testing.T) {
	m := openMgr(t, Options{})
	putKey(t, m, "k", "0")
	a := begin(t, m, false)
	b := begin(t, m, false)
	if err := a.Put(storage.KSNode, []byte("k"), []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(storage.KSNode, []byte("k"), []byte("b")); err != nil {
		t.Fatal(err)
	}
	if err := a.Commit(); err != nil {
		t.Fatal(err)
	}
	err := b.Commit()
	if !gerr.IsCode(err, gerr.Conflict) || !gerr.Retryable(err) {
		t.Fatalf("lost update: %v", err)
	}
	if err := b.Rollback(); err != nil {
		t.Fatal(err)
	}
	if string(getKey(t, m, "k")) != "a" {
		t.Fatalf("winner: %q", getKey(t, m, "k"))
	}
}

func TestWriteSkewAllowed(t *testing.T) {
	// Snapshot isolation allows write skew. Each transaction reads a+b >= 10
	// and then withdraws 10 from a different account. Both commit. A later
	// SELECT … FOR UPDATE lock would reject one of them; that is future work.
	m := openMgr(t, Options{})
	putKey(t, m, "a", "5")
	putKey(t, m, "b", "5")
	t1 := begin(t, m, false)
	t2 := begin(t, m, false)
	if sum := atoi(getTx(t, t1, "a")) + atoi(getTx(t, t1, "b")); sum < 10 {
		t.Fatal(sum)
	}
	if sum := atoi(getTx(t, t2, "a")) + atoi(getTx(t, t2, "b")); sum < 10 {
		t.Fatal(sum)
	}
	if err := t1.Put(storage.KSNode, []byte("a"), []byte("-5")); err != nil {
		t.Fatal(err)
	}
	if err := t2.Put(storage.KSNode, []byte("b"), []byte("-5")); err != nil {
		t.Fatal(err)
	}
	if err := t1.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := t2.Commit(); err != nil {
		t.Fatalf("write skew should commit: %v", err)
	}
	if string(getKey(t, m, "a")) != "-5" || string(getKey(t, m, "b")) != "-5" {
		t.Fatalf("skew result a=%q b=%q", getKey(t, m, "a"), getKey(t, m, "b"))
	}
}

func TestUniquePhantom(t *testing.T) {
	m := openMgr(t, Options{})
	a := begin(t, m, false).(*Tx)
	b := begin(t, m, false).(*Tx)
	if err := a.InsertUnique(storage.KSProp, []byte("email:ada"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := b.InsertUnique(storage.KSProp, []byte("email:ada"), []byte("2")); err != nil {
		t.Fatal(err)
	}
	if err := a.Commit(); err != nil {
		t.Fatal(err)
	}
	err := b.Commit()
	if !gerr.IsCode(err, gerr.ConstraintViolation) || gerr.Retryable(err) {
		t.Fatalf("phantom: %v", err)
	}
	if err := b.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestVacuumReclaimsVersions(t *testing.T) {
	m := openMgr(t, Options{})
	putKey(t, m, "a", "1")
	r := begin(t, m, true)
	putKey(t, m, "a", "2")
	n, err := m.HistoricVersions()
	if err != nil || n != 1 {
		t.Fatalf("chain: %d %v", n, err)
	}
	got, err := m.Vacuum(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("vacuum removed a version a reader still needs: %d", got)
	}
	val, err := r.Get(storage.KSNode, []byte("a"))
	if err != nil || string(val) != "1" {
		t.Fatalf("reader after vacuum: %q %v", val, err)
	}
	if err := r.Rollback(); err != nil {
		t.Fatal(err)
	}
	got, err = m.Vacuum(context.Background())
	if err != nil || got == 0 {
		t.Fatalf("vacuum: %d %v", got, err)
	}
	n, err = m.HistoricVersions()
	if err != nil || n != 0 {
		t.Fatalf("chain left: %d %v", n, err)
	}
	st := m.MVCCStats()
	if st.VersionsReclaimed == 0 {
		t.Fatalf("stats: %+v", st)
	}
	if string(getKey(t, m, "a")) != "2" {
		t.Fatal(getKey(t, m, "a"))
	}
}

func TestLimits(t *testing.T) {
	m := openMgr(t, Options{MaxOpenTxns: 1})
	tx := begin(t, m, false)
	if _, err := m.Begin(storage.TxOptions{}); !gerr.IsCode(err, gerr.ResourceExhausted) {
		t.Fatalf("max open: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	m = openMgr(t, Options{MaxWriteSet: 1})
	tx = begin(t, m, false)
	if err := tx.Put(storage.KSNode, []byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSNode, []byte("b"), []byte("2")); !gerr.IsCode(err, gerr.ResourceExhausted) {
		t.Fatalf("write set: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	m = openMgr(t, Options{IdleTimeout: 15 * time.Millisecond})
	tx = begin(t, m, false)
	if err := tx.Put(storage.KSNode, []byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if err := tx.Commit(); !gerr.IsCode(err, gerr.ResourceExhausted) {
		t.Fatalf("idle: %v", err)
	}
}

func TestMigrateRawValues(t *testing.T) {
	eng := memory.Open()
	tx, err := eng.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSNode, []byte("a"), []byte("raw")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	m, err := Open(eng, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if string(getKey(t, m, "a")) != "raw" {
		t.Fatalf("migrated: %q", getKey(t, m, "a"))
	}
}

func TestAbortRecord(t *testing.T) {
	inner := memory.Open()
	lg := &abortLog{Engine: inner}
	m, err := Open(lg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	tx := begin(t, m, false)
	if err := tx.Put(storage.KSNode, []byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if lg.n != 1 {
		t.Fatalf("aborts: %d", lg.n)
	}
	ro := begin(t, m, true)
	if err := ro.Rollback(); err != nil {
		t.Fatal(err)
	}
	if lg.n != 1 {
		t.Fatalf("read-only rollback logged an abort: %d", lg.n)
	}
}

func TestGraphstoreVersions(t *testing.T) {
	m := openMgr(t, Options{})
	tx := begin(t, m, false)
	n, err := graphstore.CreateNode(tx, []string{"person"}, map[string]value.Value{"name": value.String("Ada")})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	r := begin(t, m, true)
	w := begin(t, m, false)
	if _, err := graphstore.UpdateNode(w, n.ID, map[string]value.Value{"name": value.String("Ada Lovelace")}); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := graphstore.GetNode(r, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := got.Props[0].Value.StringValue()
	if !ok || s != "Ada" {
		t.Fatalf("old snapshot: %+v", got.Props)
	}
	if err := r.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestNewerFormatRefused(t *testing.T) {
	eng := memory.Open()
	tx, err := eng.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw := encodeOracle(oracle{nextTxn: 1, commitTS: 1})
	raw[0] = formatVersion + 1
	if err := tx.Put(storage.KSVersion, metaKey, raw); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(eng, Options{}); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("newer format: %v", err)
	}
	_ = eng.Close()
}

type abortLog struct {
	storage.Engine
	n int
}

func (a *abortLog) LogAbort(uint64) error {
	a.n++
	return nil
}

func openMgr(t *testing.T, opt Options) *Manager {
	t.Helper()
	m, err := Open(memory.Open(), opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func begin(t *testing.T, m *Manager, ro bool) storage.Tx {
	t.Helper()
	tx, err := m.Begin(storage.TxOptions{ReadOnly: ro})
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func putKey(t *testing.T, m *Manager, key, val string) {
	t.Helper()
	tx := begin(t, m, false)
	if err := tx.Put(storage.KSNode, []byte(key), []byte(val)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func getKey(t *testing.T, m *Manager, key string) []byte {
	t.Helper()
	tx := begin(t, m, true)
	defer func() { _ = tx.Rollback() }()
	got, err := tx.Get(storage.KSNode, []byte(key))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func getTx(t *testing.T, tx storage.Tx, key string) string {
	t.Helper()
	got, err := tx.Get(storage.KSNode, []byte(key))
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

func atoi(s string) int {
	n := 0
	sign := 1
	for i, c := range s {
		if i == 0 && c == '-' {
			sign = -1
			continue
		}
		n = n*10 + int(c-'0')
	}
	return sign * n
}
