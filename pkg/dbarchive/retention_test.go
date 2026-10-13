// adr: 1027
package dbarchive

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)

func segName(n int) string { return "00000001000000000000000" + string(rune('0'+n)) }

func putFile(t *testing.T, s *LocalStore, db, name, body string) {
	t.Helper()
	if err := s.PutWAL(context.Background(), db, name, strings.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("put %s: %v", name, err)
	}
}

// putBackup stores a backup starting in segment n at start and, when stop
// is set, the history file PostgreSQL archives at pg_backup_stop.
func putBackup(t *testing.T, s *LocalStore, db string, n int, start time.Time, stop bool) BackupManifest {
	t.Helper()
	lsn := "0/" + string(rune('0'+n)) + "000028"
	m, err := s.PutBackup(context.Background(), db, bytes.NewReader(baseTar(t, lsn, segName(n), start, "rows")), start)
	if err != nil {
		t.Fatal(err)
	}
	if stop {
		putFile(t, s, db, segName(n)+".00000028.backup", history(start.Add(time.Minute)))
	}
	return m
}

func localFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// TestPruneKeepsEverythingTheRetentionNeeds: the newest backup consistent
// before the cutoff anchors the archive; older backups, WAL before its
// start segment and their history files go, locally and in the bucket.
func TestPruneKeepsEverythingTheRetentionNeeds(t *testing.T) {
	local := newStore(t)
	remote := &memRemote{objects: map[string][]byte{}}
	store := &TieredStore{LocalStore: local, Remote: remote, Prefix: "native"}
	ctx := context.Background()
	old := putBackup(t, local, dbA, 2, t0, true)
	anchor := putBackup(t, local, dbA, 5, t0.Add(time.Hour), true)
	newest := putBackup(t, local, dbA, 9, t0.Add(2*time.Hour), false) // history not archived yet
	for n := 1; n <= 9; n++ {
		putFile(t, local, dbA, segName(n), "wal")
	}
	putFile(t, local, dbA, segName(3)+".partial", "partial")
	putFile(t, local, dbA, "00000002.history", "1\t0/3000000\tno recovery target specified\n")
	if _, err := (&Uploader{Store: local, Remote: remote, Prefix: "native"}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	// A cutoff before every stop time anchors nothing.
	if res, err := store.Prune(ctx, dbA, t0); err != nil || res != (PruneResult{}) {
		t.Fatalf("early prune = %+v, %v", res, err)
	}
	res, err := store.Prune(ctx, dbA, t0.Add(90*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	// Segments 1-4, the partial and the old backup's history file.
	if res.Backups != 1 || res.WAL != 6 {
		t.Fatalf("prune = %+v", res)
	}
	wal := strings.Join(localFiles(t, filepath.Join(local.dir, dbA, "wal")), " ")
	for _, gone := range []string{segName(1), segName(4), segName(3) + ".partial", segName(2) + ".00000028.backup"} {
		if strings.Contains(wal, gone) || remote.has(WALKey("native", dbA, gone)) {
			t.Errorf("%s survived", gone)
		}
	}
	for _, kept := range []string{segName(5), segName(9), segName(5) + ".00000028.backup", "00000002.history"} {
		if !strings.Contains(wal, kept) || !remote.has(WALKey("native", dbA, kept)) {
			t.Errorf("%s was pruned", kept)
		}
	}
	if markers := localFiles(t, filepath.Join(local.dir, dbA, "uploaded")); strings.Contains(strings.Join(markers, " "), segName(1)) {
		t.Errorf("upload marker of a pruned file survived: %v", markers)
	}
	backups, err := local.ListBackups(dbA)
	if err != nil || len(backups) != 2 || backups[0].ID != anchor.ID || backups[1].ID != newest.ID {
		t.Fatalf("backups after prune = %+v, %v", backups, err)
	}
	for _, name := range []string{old.ID + ".json", old.ID + ".tar.zst"} {
		if remote.has(BaseKey("native", dbA, name)) {
			t.Errorf("bucket kept %s", name)
		}
	}
	// The cutoff is still restorable; a pass with nothing older is a no-op.
	if m, err := local.SelectBackup(ctx, store, dbA, t0.Add(90*time.Minute)); err != nil || m.ID != anchor.ID {
		t.Fatalf("restore to the cutoff selects %+v, %v", m, err)
	}
	if res, err := store.Prune(ctx, dbA, t0.Add(90*time.Minute)); err != nil || res != (PruneResult{}) {
		t.Fatalf("second prune = %+v, %v", res, err)
	}
}

// A bucket outage stops the pass before anything local goes, so the next
// pass retries the same files.
func TestPruneKeepsLocalFilesWhenTheBucketFails(t *testing.T) {
	local := newStore(t)
	remote := &memRemote{objects: map[string][]byte{}, fail: errors.New("bucket down")}
	store := &TieredStore{LocalStore: local, Remote: remote, Prefix: "native"}
	putBackup(t, local, dbA, 2, t0, true)
	putBackup(t, local, dbA, 5, t0.Add(time.Hour), true)
	if _, err := store.Prune(context.Background(), dbA, t0.Add(2*time.Hour)); err == nil {
		t.Fatal("prune ignored a bucket failure")
	}
	if backups, _ := local.ListBackups(dbA); len(backups) != 2 {
		t.Fatalf("local backups after a failed prune = %+v", backups)
	}
}

func TestPruneAllUsesTheWindowOrTheMinimum(t *testing.T) {
	local := newStore(t)
	store := &TieredStore{LocalStore: local}
	for _, db := range []string{dbA, dbB} {
		putBackup(t, local, db, 2, t0, true)
		putBackup(t, local, db, 5, t0.Add(time.Hour), true)
		putBackup(t, local, db, 9, t0.Add(2*time.Hour), true)
	}
	if err := local.NoteWindow(dbA, int64((2 * time.Hour).Seconds())); err != nil {
		t.Fatal(err)
	}
	now := t0.Add(3*time.Hour + 30*time.Minute)
	res, err := store.PruneAll(context.Background(), now, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// dbA keeps two hours (the 1 h and 2 h backups); dbB only the minimum.
	a, _ := local.ListBackups(dbA)
	b, _ := local.ListBackups(dbB)
	if res.Backups != 3 || len(a) != 2 || len(b) != 1 {
		t.Fatalf("prune all = %+v; dbA %d backups, dbB %d", res, len(a), len(b))
	}
}

func TestNoteWindowPersists(t *testing.T) {
	local := newStore(t)
	if local.Window(dbA) != 0 {
		t.Fatal("window before any was noted")
	}
	if err := local.NoteWindow(dbA, 3600); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewLocalStore(local.dir)
	if err != nil || reopened.Window(dbA) != 3600 {
		t.Fatalf("window after reopen = %d, %v", reopened.Window(dbA), err)
	}
	if err := local.NoteWindow("../x", 1); err == nil {
		t.Fatal("a window for an invalid id was written")
	}
}

func TestDropRemovesTheArchiveLocallyAndInTheBucket(t *testing.T) {
	local := newStore(t)
	remote := &memRemote{objects: map[string][]byte{}}
	store := &TieredStore{LocalStore: local, Remote: remote, Prefix: "native"}
	ctx := context.Background()
	putBackup(t, local, dbA, 2, t0, true)
	putFile(t, local, dbA, segName(2), "wal")
	putFile(t, local, dbB, segName(2), "wal")
	if _, err := (&Uploader{Store: local, Remote: remote, Prefix: "native"}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	remote.objects["native/"+dbA+"/wal/"+segName(1)] = []byte("only in the bucket")
	if err := store.Drop(ctx, dbA); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(local.dir, dbA)); !os.IsNotExist(err) {
		t.Fatalf("local archive survived: %v", err)
	}
	if keys, _ := remote.List(ctx, "native/"+dbA+"/"); len(keys) != 0 {
		t.Fatalf("bucket kept %v", keys)
	}
	if !remote.has(WALKey("native", dbB, segName(2))) {
		t.Fatal("another database's archive was dropped")
	}
}

// Only vmmd's admin stream drops; a relayed guest stream cannot, and the
// relay header carries the window into the store.
func TestDropNeedsTheAdminHeader(t *testing.T) {
	local := newStore(t)
	store := &TieredStore{LocalStore: local}
	server := &Server{Store: store, Backups: store, Retention: store}
	sock := serveSocket(t, server)
	dial := func(h Header) net.Conn {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if err := WriteLine(conn, h); err != nil {
			t.Fatal(err)
		}
		return conn
	}
	guest := NewClient(dial(Header{DatabaseID: dbA, RestoreWindowSeconds: 7200}))
	if err := guest.PutWAL(seg, strings.NewReader("wal"), 3); err != nil {
		t.Fatal(err)
	}
	if local.Window(dbA) != 7200 {
		t.Fatalf("window from the relay header = %d", local.Window(dbA))
	}
	conn := dial(Header{DatabaseID: dbA})
	if err := WriteLine(conn, Request{Op: OpDrop}); err != nil {
		t.Fatal(err)
	}
	var resp Response
	if err := ReadLine(bufio.NewReader(conn), &resp); err != nil || resp.Error != ErrCodeInvalid {
		t.Fatalf("guest drop = %+v, %v", resp, err)
	}
	if _, err := os.Stat(filepath.Join(local.dir, dbA, "wal", seg)); err != nil {
		t.Fatalf("a guest stream dropped the archive: %v", err)
	}
	if err := (Header{DatabaseID: dbA, Admin: true, RestoreSource: dbB}).Validate(); err == nil {
		t.Fatal("an admin header naming a restore source validated")
	}
	if err := DropArchive(context.Background(), sock, dbA); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(local.dir, dbA)); !os.IsNotExist(err) {
		t.Fatalf("admin drop left the archive: %v", err)
	}
}
