package livequery

import (
	"context"
	"testing"
	"time"
)

// adr: 20261016151947 — capture decodes committed transactions only, in commit order,
// and a non-transactional marker arrives after everything committed before it.
func TestCaptureDecodesCommittedTransactionsAndMarkers(t *testing.T) {
	dsn, pool := openLogicalDB(t)
	mustExec(t, pool, `CREATE TABLE items (id bigint PRIMARY KEY, name text, note text)`)
	mustExec(t, pool, `CREATE PUBLICATION lq_pub FOR ALL TABLES`)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	capture, err := OpenCapture(ctx, dsn, "lq_capture_test", "lq_pub")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = capture.Close() }()

	mustExec(t, pool, `INSERT INTO items VALUES (1, 'a', NULL), (2, 'b', 'x')`)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO items VALUES (3, 'rolled back', NULL)`); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	mustExec(t, pool, `UPDATE items SET id = 10, name = 'a2' WHERE id = 1`)
	mustExec(t, pool, `UPDATE items SET note = 'y' WHERE id = 2`)
	mustExec(t, pool, `DELETE FROM items WHERE id = 2`)
	mustExec(t, pool, `SELECT pg_logical_emit_message(false, $1, 'm1')`, MarkerPrefix)

	var txns []*Txn
	for done := false; !done; {
		select {
		case ev, ok := <-capture.Events():
			if !ok {
				t.Fatalf("capture stopped: %v", capture.Err())
			}
			if ev.Txn != nil {
				txns = append(txns, ev.Txn)
			} else if ev.Marker == "m1" {
				done = true
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for marker")
		}
	}
	if len(txns) != 4 {
		t.Fatalf("committed transactions = %d, want 4 (rollback must not appear)", len(txns))
	}
	insert := txns[0].Changes
	if len(insert) != 2 || insert[0].Op != 'I' || *insert[0].New["id"] != "1" || insert[0].New["note"] != nil {
		t.Fatalf("insert changes = %+v", insert)
	}
	keyChange := txns[1].Changes[0]
	if keyChange.Op != 'U' || *keyChange.Old["id"] != "1" || *keyChange.New["id"] != "10" {
		t.Fatalf("primary-key update = %+v, want old key 1 and new key 10", keyChange)
	}
	plain := txns[2].Changes[0]
	if plain.Op != 'U' || plain.Old != nil || *plain.New["id"] != "2" {
		t.Fatalf("non-key update = %+v, want no old tuple and new key 2", plain)
	}
	del := txns[3].Changes[0]
	if del.Op != 'D' || *del.Old["id"] != "2" || del.New != nil {
		t.Fatalf("delete = %+v", del)
	}
	for i := 1; i < len(txns); i++ {
		if txns[i].EndLSN <= txns[i-1].EndLSN {
			t.Fatalf("transactions out of commit order: %v then %v", txns[i-1].EndLSN, txns[i].EndLSN)
		}
	}
}

func TestSnapshotVisibilityAndFullXid(t *testing.T) {
	snap, err := ParseSnapshot("100:110:103,107")
	if err != nil {
		t.Fatal(err)
	}
	for xid, want := range map[uint64]bool{99: true, 100: true, 103: false, 105: true, 107: false, 110: false, 200: false} {
		if got := snap.Visible(xid); got != want {
			t.Fatalf("Visible(%d) = %v, want %v", xid, got, want)
		}
	}
	ref := uint64(5)<<32 | 10
	for _, tc := range []struct {
		xid  uint32
		want uint64
	}{
		{5, uint64(5)<<32 | 5},
		{10, uint64(5)<<32 | 10},
		{0xfffffff0, uint64(4)<<32 | 0xfffffff0},
	} {
		if got := FullXid(tc.xid, ref); got != tc.want {
			t.Fatalf("FullXid(%d) = %d, want %d", tc.xid, got, tc.want)
		}
	}
	if got := FullXid(7, 20); got != 7 {
		t.Fatalf("FullXid in epoch 0 = %d, want 7", got)
	}
}
