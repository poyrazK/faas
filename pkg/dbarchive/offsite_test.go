// adr: 1027 — the off-site mirror keeps a second copy of every archive at
// another provider: it never holds up the bucket, deletes only what the
// store deleted, follows holds, drops and handoffs, and serves restores
// when the bucket is gone.
package dbarchive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const holdKeep = "0123456789abcdef0123456789abcdef"

// downRemote is a bucket that is gone: every call fails.
type downRemote struct{}

var errBucketGone = errors.New("bucket gone")

func (downRemote) Put(context.Context, string, io.ReadSeeker, int64) error { return errBucketGone }
func (downRemote) Get(context.Context, string, io.Writer) error            { return errBucketGone }
func (downRemote) Delete(context.Context, string) error                    { return errBucketGone }
func (downRemote) List(context.Context, string) ([]string, error)          { return nil, errBucketGone }
func (downRemote) Sizes(context.Context, string) (map[string]int64, error) {
	return nil, errBucketGone
}

// mirrored is a store with a bucket and an off-site mirror.
func mirrored(t *testing.T) (*TieredStore, *Uploader, *Offsite, *memRemote, *memRemote) {
	t.Helper()
	local := newStore(t)
	bucket, mirror := &memRemote{objects: map[string][]byte{}}, &memRemote{objects: map[string][]byte{}}
	offsite := NewOffsite(local, bucket, "native", mirror, "dr")
	store := &TieredStore{LocalStore: local, Remote: bucket, Prefix: "native", Offsite: mirror, OffsitePrefix: "dr"}
	return store, &Uploader{Store: local, Remote: bucket, Prefix: "native"}, offsite, bucket, mirror
}

func TestOffsiteMirrorsWithoutHoldingUpTheBucket(t *testing.T) {
	store, uploader, offsite, bucket, mirror := mirrored(t)
	ctx := context.Background()
	backup := putBackup(t, store.LocalStore, dbA, 2, t0, true)
	putFile(t, store.LocalStore, dbA, segName(2), "wal-2")
	var pending int
	var oldest time.Duration
	offsite.OnPending = func(n int, age time.Duration) { pending, oldest = n, age }

	// The mirror is down; the bucket still gets everything.
	mirror.fail = errors.New("off-site provider down")
	if _, err := offsite.RunOnce(ctx); err == nil || pending != 4 || oldest <= 0 {
		t.Fatalf("with the mirror down: %v, pending %d, oldest %v", err, pending, oldest)
	}
	if n, err := uploader.RunOnce(ctx); err != nil || n != 4 {
		t.Fatalf("bucket upload = %d, %v", n, err)
	}
	mirror.fail = nil
	if n, err := offsite.RunOnce(ctx); err != nil || n != 4 || pending != 0 {
		t.Fatalf("mirror pass = %d, %v, pending %d", n, err, pending)
	}
	if n, _ := offsite.RunOnce(ctx); n != 0 || mirror.puts != 4 {
		t.Fatalf("a second pass re-uploaded (%d, puts %d)", n, mirror.puts)
	}
	for _, rel := range []string{"wal/" + segName(2), "base/" + backup.ID + ".tar.zst", "base/" + backup.ID + ".json"} {
		want, got := bucket.objects["native/"+dbA+"/"+rel], mirror.objects["dr/"+dbA+"/"+rel]
		if len(want) == 0 || !bytes.Equal(want, got) {
			t.Fatalf("%s: mirror holds %d bytes, bucket %d", rel, len(got), len(want))
		}
	}
}

func TestOffsiteFollowsPrunesHoldsAndDrops(t *testing.T) {
	store, uploader, offsite, _, mirror := mirrored(t)
	ctx := context.Background()
	putBackup(t, store.LocalStore, dbA, 2, t0, true)
	putBackup(t, store.LocalStore, dbA, 5, t0.Add(time.Hour), true)
	for n := 1; n <= 6; n++ {
		putFile(t, store.LocalStore, dbA, segName(n), "wal")
	}
	for _, pass := range []func(context.Context) (int, error){uploader.RunOnce, offsite.RunOnce} {
		if _, err := pass(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Prune(ctx, dbA, t0.Add(90*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutHold(ctx, dbA, holdKeep, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Pruned files leave the mirror only once its pass replays the journal.
	if !mirror.has(WALKey("dr", dbA, segName(1))) {
		t.Fatal("the mirror deleted before its pass")
	}
	if _, err := offsite.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 4; n++ {
		if mirror.has(WALKey("dr", dbA, segName(n))) {
			t.Errorf("pruned %s survived in the mirror", segName(n))
		}
	}
	if !mirror.has(WALKey("dr", dbA, segName(5))) || !mirror.has(HoldKey("dr", dbA, holdKeep)) {
		t.Fatal("the mirror lost a kept segment or missed the hold")
	}
	if err := store.DeleteHold(ctx, dbA, holdKeep); err != nil {
		t.Fatal(err)
	}
	if err := store.Drop(ctx, dbA); err != nil {
		t.Fatal(err)
	}
	if _, err := offsite.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if keys, _ := mirror.List(ctx, "dr/"+dbA+"/"); len(keys) != 0 {
		t.Fatalf("the mirror kept a dropped archive: %v", keys)
	}
	if entries, _ := offsite.journal(); len(entries) != 0 {
		t.Fatalf("journal left %v", entries)
	}
	if _, err := os.Stat(filepath.Join(store.dir, dbA)); !os.IsNotExist(err) {
		t.Fatalf("the mirror resurrected a dropped database's directory: %v", err)
	}

	// Without a mirror the store journals nothing.
	plain := &TieredStore{LocalStore: newStore(t), Remote: &memRemote{objects: map[string][]byte{}}, Prefix: "native"}
	putFile(t, plain.LocalStore, dbA, segName(1), "wal")
	if err := plain.Drop(ctx, dbA); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(plain.dir, offsiteJournalDir)); !os.IsNotExist(err) {
		t.Fatalf("a store without a mirror journaled: %v", err)
	}
}

// An emptied bucket (a lost account) is no deletion: the mirror keeps
// everything the store never deleted.
func TestOffsiteNeverInfersDeletionsFromTheBucket(t *testing.T) {
	store, uploader, offsite, bucket, mirror := mirrored(t)
	ctx := context.Background()
	putBackup(t, store.LocalStore, dbA, 2, t0, true)
	for _, pass := range []func(context.Context) (int, error){uploader.RunOnce, offsite.RunOnce} {
		if _, err := pass(ctx); err != nil {
			t.Fatal(err)
		}
	}
	before := len(mirror.objects)
	bucket.objects = map[string][]byte{}
	if _, err := offsite.RunOnce(ctx); err != nil || len(mirror.objects) != before || before == 0 {
		t.Fatalf("after the bucket emptied: %v, mirror %d objects, was %d", err, len(mirror.objects), before)
	}
}

// A handoff releases the node's copy; what the mirror had not uploaded
// from here it copies from the bucket.
func TestOffsiteCopiesWhatAHandoffReleased(t *testing.T) {
	store, uploader, offsite, _, mirror := mirrored(t)
	ctx := context.Background()
	putFile(t, store.LocalStore, dbA, segName(1), "wal-1")
	if _, err := offsite.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	putFile(t, store.LocalStore, dbA, segName(2), "wal-2")
	if err := uploader.Release(ctx, dbA); err != nil {
		t.Fatal(err)
	}
	if names, _ := offsite.journal(); len(names) != 1 {
		t.Fatalf("journal after the release = %v; want one copy, of the file not yet mirrored", names)
	}
	if _, err := offsite.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if string(mirror.objects[WALKey("dr", dbA, segName(2))]) != "wal-2" || !mirror.has(WALKey("dr", dbA, segName(1))) {
		t.Fatalf("mirror after the release = %v", mirror.objects)
	}
}

// Entries keep their order across a clock stepped back and a restart.
func TestOffsiteJournalOrderSurvivesAClockStepAndARestart(t *testing.T) {
	store, _, _, _, _ := mirrored(t)
	future := time.Now().Add(time.Hour).UnixNano()
	store.offsiteLast.Store(future)
	if err := store.journalOffsite(offsiteOp{Op: offsiteCopy, Database: dbA, Path: "holds/" + holdKeep}); err != nil {
		t.Fatal(err)
	}
	restarted := NewOffsite(&LocalStore{dir: store.dir}, nil, "native", nil, "dr")
	if err := restarted.Store.journalOffsite(offsiteOp{Op: offsiteDelete, Database: dbA, Path: "holds/" + holdKeep}); err != nil {
		t.Fatal(err)
	}
	names, err := restarted.journal()
	if err != nil || len(names) != 2 {
		t.Fatalf("journal = %v, %v", names, err)
	}
	var ops []string
	for _, name := range names {
		raw, _ := os.ReadFile(filepath.Join(store.dir, offsiteJournalDir, name))
		ops = append(ops, string(raw))
	}
	if !strings.Contains(ops[0], `"copy"`) || !strings.Contains(ops[1], `"delete"`) {
		t.Fatalf("journal order = %v; want the copy before the delete", ops)
	}
}

// A journal entry the store did not write is never applied.
func TestOffsiteStopsAtACorruptJournalEntry(t *testing.T) {
	store, _, offsite, _, mirror := mirrored(t)
	ctx := context.Background()
	mirror.objects["dr/"+dbA+"/wal/"+segName(1)] = []byte("kept")
	dir := filepath.Join(store.dir, offsiteJournalDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	entry := `{"op":"delete","database":"` + dbA + `","path":"../../etc"}`
	if err := os.WriteFile(filepath.Join(dir, "00000000000000000001.json"), []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := offsite.RunOnce(ctx); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("pass = %v; want it stopped at the corrupt entry", err)
	}
	if !mirror.has("dr/" + dbA + "/wal/" + segName(1)) {
		t.Fatal("a corrupt entry deleted an object")
	}
}

// With the bucket gone, a node with an empty disk restores another
// database entirely from the mirror.
func TestRestoreFromTheMirrorWhenTheBucketIsGone(t *testing.T) {
	ctx := context.Background()
	lost := newStore(t)
	mirror := &FileRemote{Dir: t.TempDir()}
	offsite := NewOffsite(lost, &memRemote{objects: map[string][]byte{}}, "native", mirror, "dr")
	first := putBackup(t, lost, dbA, 2, t0, true)
	putFile(t, lost, dbA, segName(2), "wal-2")
	if _, err := offsite.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	want, err := lost.OpenBackup(ctx, dbA, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantTar, _ := io.ReadAll(want)
	_ = want.Close()

	fresh := &TieredStore{LocalStore: newStore(t), Remote: downRemote{}, Prefix: "native", Offsite: mirror, OffsitePrefix: "dr"}
	dial := serveServer(t, &Server{Store: fresh, Backups: fresh})
	var got bytes.Buffer
	id, err := dial(Header{DatabaseID: dbB, RestoreSource: dbA}).GetBackup(t0.Add(30*time.Minute), &got)
	if err != nil || id != first.ID || !bytes.Equal(got.Bytes(), wantTar) {
		t.Fatalf("restore from the mirror = %q (%d bytes), %v", id, got.Len(), err)
	}
	var wal bytes.Buffer
	if err := dial(Header{DatabaseID: dbB, RestoreSource: dbA}).GetWAL(segName(2), &wal); err != nil || wal.String() != "wal-2" {
		t.Fatalf("wal from the mirror = %q, %v", wal.String(), err)
	}
}
