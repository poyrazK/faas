// adr: 1027 — the restore-history meter sizes a database's whole archive:
// after a move or a failover its earlier history is only in the bucket.
package dbarchive

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestArchiveBytesCountsTheBucketAndTheNodeOnce(t *testing.T) {
	t0 := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	for name, remote := range map[string]Remote{
		"mem":  &memRemote{objects: map[string][]byte{}},
		"file": &FileRemote{Dir: t.TempDir()},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			// The old node archived a base backup and a segment, shipped
			// them, and the database moved away.
			old := newStore(t)
			putBackup(t, old, dbA, 2, t0, true)
			putFile(t, old, dbA, segName(2), "wal-2")
			if _, err := (&Uploader{Store: old, Remote: remote, Prefix: "native"}).RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			history, err := old.LocalBytes(dbA)
			if err != nil || history == 0 {
				t.Fatalf("old node's archive = %d, %v", history, err)
			}

			// The new node shipped one segment (and keeps its copy) and has
			// one not uploaded yet; a hold is not history.
			moved := &TieredStore{LocalStore: newStore(t), Remote: remote, Prefix: "native"}
			putFile(t, moved.LocalStore, dbA, segName(3), "wal-3")
			if _, err := (&Uploader{Store: moved.LocalStore, Remote: remote, Prefix: "native"}).RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			putFile(t, moved.LocalStore, dbA, segName(4), "wal-4-pending")
			if _, err := moved.PutHold(ctx, dbA, "0123456789abcdef0123456789abcdef", t0); err != nil {
				t.Fatal(err)
			}
			want := history + int64(len("wal-3")+len("wal-4-pending"))
			if got, err := moved.ArchiveBytes(ctx, dbA); err != nil || got != want {
				t.Fatalf("archive bytes = %d, %v; want %d", got, err, want)
			}
			if local, _ := moved.LocalBytes(dbA); local >= want {
				t.Fatalf("local bytes %d already cover the history; the test proves nothing", local)
			}

			// dbbackupd's size op answers the same.
			sock := serveSocket(t, &Server{Store: moved, Backups: moved, Retention: moved})
			if got, err := ArchiveBytes(ctx, sock, dbA); err != nil || got != want {
				t.Fatalf("size op = %d, %v; want %d", got, err, want)
			}

			// Without a bucket the node's archive is the whole archive.
			alone := &TieredStore{LocalStore: old}
			if got, err := alone.ArchiveBytes(ctx, dbA); err != nil || got != history {
				t.Fatalf("archive bytes without a bucket = %d, %v; want %d", got, err, history)
			}
		})
	}
}

// A bucket that cannot be listed leaves the database unsampled rather
// than sampled short.
func TestArchiveBytesFailsWithTheBucket(t *testing.T) {
	store := &TieredStore{LocalStore: newStore(t), Remote: &memRemote{objects: map[string][]byte{}, fail: errors.New("bucket down")}, Prefix: "native"}
	putFile(t, store.LocalStore, dbA, segName(1), "wal-1")
	if _, err := store.ArchiveBytes(context.Background(), dbA); err == nil {
		t.Fatal("sized the archive without its bucket")
	}
}
