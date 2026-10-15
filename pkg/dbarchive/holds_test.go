// adr: 1027
package dbarchive

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const holdA = "0123456789abcdef0123456789abcdef"

// A hold keeps its point restorable after the retention period has passed
// it, with or without a bucket; removing it lets the next pass prune.
func TestHoldKeepsItsPointRestorable(t *testing.T) {
	for _, bucket := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "bucket"}[bucket], func(t *testing.T) {
			ctx := context.Background()
			local := newStore(t)
			store := &TieredStore{LocalStore: local, Prefix: "native"}
			var remote *memRemote
			if bucket {
				remote = &memRemote{objects: map[string][]byte{}}
				store.Remote = remote
			}
			first := putBackup(t, local, dbA, 2, t0, true)
			putBackup(t, local, dbA, 5, t0.Add(time.Hour), true)
			putBackup(t, local, dbA, 9, t0.Add(2*time.Hour), true)
			for n := 1; n <= 9; n++ {
				putFile(t, local, dbA, segName(n), "wal")
			}
			if bucket {
				if _, err := (&Uploader{Store: local, Remote: remote, Prefix: "native"}).RunOnce(ctx); err != nil {
					t.Fatal(err)
				}
			}
			point := t0.Add(30 * time.Minute)
			hold, err := store.PutHold(ctx, dbA, holdA, point)
			if err != nil || !hold.PointInTime.Equal(point) || hold.CreatedAt.Before(point) {
				t.Fatalf("hold = %+v, %v", hold, err)
			}
			if bucket && !remote.has(HoldKey("native", dbA, holdA)) {
				t.Fatal("the hold is not in the bucket")
			}
			// The retention period starts after every backup but the newest;
			// only the segment before the first backup goes.
			if res, err := store.Prune(ctx, dbA, t0.Add(3*time.Hour)); err != nil || res != (PruneResult{WAL: 1}) {
				t.Fatalf("prune under the hold = %+v, %v", res, err)
			}
			if m, err := local.SelectBackup(ctx, store, dbA, point); err != nil || m.ID != first.ID {
				t.Fatalf("restore to the held point selects %+v, %v", m, err)
			}
			if err := store.DeleteHold(ctx, dbA, holdA); err != nil {
				t.Fatal(err)
			}
			if _, err := store.GetHold(ctx, dbA, holdA); !errors.Is(err, ErrNotFound) {
				t.Fatalf("hold after delete: %v", err)
			}
			if err := store.DeleteHold(ctx, dbA, holdA); err != nil {
				t.Fatalf("second delete: %v", err)
			}
			if res, err := store.Prune(ctx, dbA, t0.Add(3*time.Hour)); err != nil || res.Backups != 2 {
				t.Fatalf("prune without the hold = %+v, %v", res, err)
			}
		})
	}
}

// Placing a hold again returns it unchanged; another point under the name
// is a conflict, and names and points are validated.
func TestPutHoldIsIdempotent(t *testing.T) {
	ctx := context.Background()
	clock := t0.Add(time.Hour)
	store := &TieredStore{LocalStore: newStore(t), now: func() time.Time { return clock }}
	first, err := store.PutHold(ctx, dbA, holdA, t0.Add(time.Nanosecond*1500))
	if err != nil || !first.CreatedAt.Equal(clock) || first.PointInTime.Nanosecond() != 1000 {
		t.Fatalf("hold = %+v, %v; want microsecond point, created now", first, err)
	}
	clock = clock.Add(time.Hour)
	again, err := store.PutHold(ctx, dbA, holdA, t0.Add(time.Microsecond))
	if err != nil || again != first {
		t.Fatalf("again = %+v, %v; want %+v", again, err, first)
	}
	if _, err := store.PutHold(ctx, dbA, holdA, t0); !errors.Is(err, ErrConflict) {
		t.Fatalf("another point: %v", err)
	}
	// A point ahead of the node's clock is created at the point.
	ahead, err := store.PutHold(ctx, dbA, strings.Repeat("1", 32), clock.Add(time.Second))
	if err != nil || !ahead.CreatedAt.Equal(ahead.PointInTime) {
		t.Fatalf("hold ahead of the clock = %+v, %v", ahead, err)
	}
	for _, bad := range []struct{ db, name string }{{dbA, "x"}, {dbA, strings.Repeat("A", 32)}, {"../" + dbA, holdA}} {
		if _, err := store.PutHold(ctx, bad.db, bad.name, t0); err == nil {
			t.Errorf("hold %q/%q accepted", bad.db, bad.name)
		}
	}
	if _, err := store.PutHold(ctx, dbA, holdA, time.Time{}); err == nil {
		t.Error("a hold without a point accepted")
	}
}

// Holds go through vmmd's admin stream only, and dropping the archive
// drops them.
func TestHoldsThroughTheAdminHeader(t *testing.T) {
	ctx := context.Background()
	remote := &memRemote{objects: map[string][]byte{}}
	store := &TieredStore{LocalStore: newStore(t), Remote: remote, Prefix: "native"}
	server := &Server{Store: store, Backups: store, Retention: store}
	sock := serveSocket(t, server)
	guest := serveServer(t, server)(Header{DatabaseID: dbA})
	if err := WriteLine(guest.rw, Request{Op: OpHold, Name: holdA, Target: t0.Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	var refused Response
	if err := ReadLine(guest.br, &refused); err != nil || refused.Error != ErrCodeInvalid {
		t.Fatalf("guest hold = %+v, %v", refused, err)
	}
	if _, err := GetArchiveHold(ctx, sock, dbA, holdA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hold before placing: %v", err)
	}
	placed, err := PutArchiveHold(ctx, sock, dbA, holdA, t0)
	if err != nil || !placed.PointInTime.Equal(t0) {
		t.Fatalf("placed = %+v, %v", placed, err)
	}
	if got, err := GetArchiveHold(ctx, sock, dbA, holdA); err != nil || got != placed {
		t.Fatalf("get = %+v, %v; want %+v", got, err, placed)
	}
	if _, err := PutArchiveHold(ctx, sock, dbA, holdA, t0.Add(time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("another point: %v", err)
	}
	if err := DropArchive(ctx, sock, dbA); err != nil {
		t.Fatal(err)
	}
	if _, err := GetArchiveHold(ctx, sock, dbA, holdA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hold after the archive was dropped: %v", err)
	}
	if _, err := PutArchiveHold(ctx, sock, dbA, holdA, t0); err != nil {
		t.Fatal(err)
	}
	if err := DeleteArchiveHold(ctx, sock, dbA, holdA); err != nil {
		t.Fatal(err)
	}
	if remote.has(HoldKey("native", dbA, holdA)) {
		t.Fatal("unhold left the bucket object")
	}
}
