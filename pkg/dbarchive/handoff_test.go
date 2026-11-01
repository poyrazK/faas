// adr: 1027 — a moving database's old node ships its archive copy to the
// bucket before the move, and drops it after.
package dbarchive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveHandoffSyncAndRelease(t *testing.T) {
	ctx := context.Background()
	local := newStore(t)
	remote := &memRemote{objects: map[string][]byte{}}
	uploader := &Uploader{Store: local, Remote: remote, Prefix: "native"}
	store := &TieredStore{LocalStore: local, Remote: remote, Prefix: "native"}
	sock := serveSocket(t, &Server{Store: store, Backups: store, Retention: store, Handoff: uploader})
	for n := 1; n <= 3; n++ {
		putFile(t, local, dbA, segName(n), "wal")
	}
	putFile(t, local, dbB, segName(1), "other database")
	if err := local.NoteWindow(dbA, 3600); err != nil {
		t.Fatal(err)
	}

	remote.fail = errors.New("bucket down")
	if _, err := SyncArchive(ctx, sock, dbA); err == nil {
		t.Fatal("sync with the bucket down succeeded")
	}
	if err := ReleaseArchive(ctx, sock, dbA); err == nil {
		t.Fatal("release with the bucket down succeeded")
	}
	if _, err := os.Stat(filepath.Join(local.dir, dbA)); err != nil {
		t.Fatalf("a failed release dropped the local copy: %v", err)
	}
	remote.fail = nil
	n, err := SyncArchive(ctx, sock, dbA)
	if err != nil || n != 3 {
		t.Fatalf("sync = %d, %v; want the three pending segments", n, err)
	}
	if remote.has(WALKey("native", dbB, segName(1))) {
		t.Fatal("sync shipped another database's archive")
	}
	if n, err := SyncArchive(ctx, sock, dbA); err != nil || n != 0 {
		t.Fatalf("second sync = %d, %v; want nothing left to ship", n, err)
	}
	putFile(t, local, dbA, segName(4), "wal")
	if err := ReleaseArchive(ctx, sock, dbA); err != nil {
		t.Fatal(err)
	}
	if !remote.has(WALKey("native", dbA, segName(4))) {
		t.Fatal("release dropped a segment the bucket never got")
	}
	if _, err := os.Stat(filepath.Join(local.dir, dbA)); !os.IsNotExist(err) {
		t.Fatalf("release kept the local copy: %v", err)
	}
	if local.Window(dbA) != 0 {
		t.Fatal("release kept the restore window")
	}
	if _, err := os.Stat(filepath.Join(local.dir, dbB)); err != nil {
		t.Fatalf("release touched another database: %v", err)
	}

	// Without a bucket there is nothing to hand off to.
	bare := serveSocket(t, &Server{Store: store, Backups: store, Retention: store})
	if _, err := SyncArchive(ctx, bare, dbA); err == nil {
		t.Fatal("sync without a bucket succeeded")
	}
	// A guest stream cannot ask for either.
	guest := serveServer(t, &Server{Store: store, Backups: store, Retention: store, Handoff: uploader})(Header{DatabaseID: dbB})
	if err := WriteLine(guest.rw, Request{Op: OpRelease}); err != nil {
		t.Fatal(err)
	}
	var refused Response
	if err := ReadLine(guest.br, &refused); err != nil || refused.Error != ErrCodeInvalid {
		t.Fatalf("guest release = %+v, %v", refused, err)
	}
}
