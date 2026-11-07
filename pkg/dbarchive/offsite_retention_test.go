// adr: 1027
package dbarchive

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func heldMirror(t *testing.T) (*TieredStore, *Uploader, *Offsite, *memRemote, *memRemote, BackupManifest) {
	t.Helper()
	store, uploader, offsite, bucket, mirror := mirrored(t)
	old := putBackup(t, store.LocalStore, dbA, 2, t0, true)
	putBackup(t, store.LocalStore, dbA, 5, t0.Add(2*time.Hour), true)
	for _, n := range []int{2, 3, 4, 5} {
		putFile(t, store.LocalStore, dbA, segName(n), "wal")
	}
	if _, err := store.PutHold(t.Context(), dbA, holdKeep, t0.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, pass := range []func(context.Context) (int, error){uploader.RunOnce, offsite.RunOnce} {
		if _, err := pass(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	return store, uploader, offsite, bucket, mirror, old
}

func TestPrunePreservesMirroredHoldAfterPrimaryMetadataLoss(t *testing.T) {
	for _, mode := range []string{"local", "bucket", "all"} {
		t.Run(mode, func(t *testing.T) {
			store, _, offsite, bucket, mirror, old := heldMirror(t)
			bucket.objects = map[string][]byte{}
			store.MinRetention = time.Hour
			store.now = func() time.Time { return t0.Add(4 * time.Hour) }
			var result PruneResult
			var err error
			switch mode {
			case "local":
				result, err = store.Prune(t.Context(), dbA, t0.Add(3*time.Hour))
			case "bucket":
				result, err = store.PruneBucket(t.Context(), dbA, 0)
			case "all":
				result, err = store.PruneAll(t.Context(), store.now(), time.Hour)
			}
			if err != nil || result != (PruneResult{}) {
				t.Fatalf("prune under mirrored hold = %+v, %v", result, err)
			}
			if _, err := offsite.RunOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			// Discard the node too: the surviving mirror must actually serve
			// the held backup, not just retain an orphaned hold record.
			cold := &TieredStore{LocalStore: newStore(t), Remote: bucket, Prefix: "native", Offsite: mirror, OffsitePrefix: "dr"}
			if got, err := cold.SelectBackup(t.Context(), cold, dbA, t0.Add(30*time.Minute)); err != nil || got.ID != old.ID {
				t.Fatalf("held restore = %+v, %v", got, err)
			}
			r, err := cold.OpenBackup(t.Context(), dbA, old.ID)
			if err != nil {
				t.Fatal(err)
			}
			n, err := io.Copy(io.Discard, r)
			_ = r.Close()
			if err != nil || n == 0 || !mirror.has(HoldKey("dr", dbA, holdKeep)) || !mirror.has(WALKey("dr", dbA, segName(2))) {
				t.Fatalf("held recovery data unavailable: bytes=%d, err=%v", n, err)
			}
		})
	}
}

type missingHoldRemote struct{ *memRemote }

func (r missingHoldRemote) Get(ctx context.Context, key string, w io.Writer) error {
	if strings.Contains(key, "/holds/") {
		return ErrNotFound
	}
	return r.memRemote.Get(ctx, key, w)
}

func TestPruneDefersForUnverifiedMirrorHolds(t *testing.T) {
	for _, failure := range []string{"list", "missing-record", "corrupt-record", "oversized-record"} {
		t.Run(failure, func(t *testing.T) {
			store, _, _, bucket, mirror, old := heldMirror(t)
			// The primary has no pins; only verified mirror state can now
			// authorize destructive pruning.
			delete(bucket.objects, HoldKey("native", dbA, holdKeep))
			switch failure {
			case "list":
				store.Offsite = downRemote{}
			case "missing-record":
				store.Offsite = missingHoldRemote{mirror}
			case "corrupt-record":
				mirror.objects[HoldKey("dr", dbA, holdKeep)] = []byte(`{"name":"wrong"}`)
			case "oversized-record":
				mirror.objects[HoldKey("dr", dbA, holdKeep)] = make([]byte, maxManifestBytes+1)
			}
			if result, err := store.Prune(t.Context(), dbA, t0.Add(3*time.Hour)); err == nil || result != (PruneResult{}) {
				t.Fatalf("unverified pins allowed prune = %+v, %v", result, err)
			}
			if backups, err := store.ListBackups(dbA); err != nil || len(backups) != 2 || !bucket.has(BaseKey("native", dbA, old.ID+".tar.zst")) {
				t.Fatalf("archive changed: %+v, %v", backups, err)
			}
		})
	}
}

func TestPruneWaitsForMirrorHoldRelease(t *testing.T) {
	store, _, offsite, _, mirror, old := heldMirror(t)
	if err := store.DeleteHold(t.Context(), dbA, holdKeep); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetHold(t.Context(), dbA, holdKeep); !errors.Is(err, ErrNotFound) {
		t.Fatalf("primary hold release = %v", err)
	}
	if result, err := store.Prune(t.Context(), dbA, t0.Add(3*time.Hour)); err != nil || result.Backups != 0 {
		t.Fatalf("pruned before mirror release: %+v, %v", result, err)
	}
	if _, err := offsite.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if result, err := store.Prune(t.Context(), dbA, t0.Add(3*time.Hour)); err != nil || result.Backups != 1 {
		t.Fatalf("prune after mirror release: %+v, %v", result, err)
	}
	if _, err := offsite.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if mirror.has(BaseKey("dr", dbA, old.ID+".tar.zst")) {
		t.Fatal("released snapshot's obsolete backup survived pruning")
	}
}

func queueMirrorDeletes(t *testing.T, store *TieredStore, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if err := store.journalOffsite(offsiteOp{Op: offsiteDelete, Database: dbA, Path: path}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOffsiteDefersQueuedDeletionOfHeldRecoveryFiles(t *testing.T) {
	store, uploader, offsite, bucket, mirror, anchor := heldMirror(t)
	older := putBackup(t, store.LocalStore, dbA, 1, t0.Add(-time.Hour), true)
	putFile(t, store.LocalStore, dbA, segName(1), "older-wal")
	putFile(t, store.LocalStore, dbA, "00000002.history", "timeline history")
	for _, pass := range []func(context.Context) (int, error){uploader.RunOnce, offsite.RunOnce} {
		if _, err := pass(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate the journal an older pruner wrote after losing primary
	// metadata. Some deletes are safe, others break the mirror's own pin.
	queueMirrorDeletes(t, store,
		"base/"+older.ID+".json", "base/"+older.ID+".tar.zst", "wal/"+segName(1), "wal/"+segName(1)+".00000028.backup",
		"base/"+anchor.ID+".json", "base/"+anchor.ID+".tar.zst", "wal/"+segName(2), "wal/"+segName(2)+".00000028.backup", "wal/00000002.history")
	bucket.objects = map[string][]byte{}
	if applied, err := offsite.RunOnce(t.Context()); err != nil || applied != 4 {
		t.Fatalf("held replay = %d, %v; want four safe deletes", applied, err)
	}
	if mirror.has(BaseKey("dr", dbA, older.ID+".tar.zst")) || !mirror.has(BaseKey("dr", dbA, anchor.ID+".tar.zst")) {
		t.Fatal("replay did not respect the held recovery anchor")
	}
	for _, path := range []string{"base/" + anchor.ID + ".json", "base/" + anchor.ID + ".tar.zst", "wal/" + segName(2), "wal/" + segName(2) + ".00000028.backup", "wal/00000002.history"} {
		if !mirror.has("dr/" + dbA + "/" + path) {
			t.Errorf("replay deleted held recovery file %s", path)
		}
	}
	if entries, _ := offsite.journal(); len(entries) != 5 {
		t.Fatalf("deferred entries = %v", entries)
	}
	// A release behind deferred deletes must run, so cleanup can resume.
	if err := store.DeleteHold(t.Context(), dbA, holdKeep); err != nil {
		t.Fatal(err)
	}
	if applied, err := offsite.RunOnce(t.Context()); err != nil || applied != 1 {
		t.Fatalf("release replay = %d, %v", applied, err)
	}
	if applied, err := offsite.RunOnce(t.Context()); err != nil || applied != 5 {
		t.Fatalf("unheld replay = %d, %v", applied, err)
	}
	if entries, _ := offsite.journal(); len(entries) != 0 {
		t.Fatalf("released deletions remained queued: %v", entries)
	}
}

func TestOffsitePreservesFilesWithoutProvenHeldAnchor(t *testing.T) {
	for _, corruptHold := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-history", true: "corrupt-hold"}[corruptHold], func(t *testing.T) {
			store, _, offsite, _, mirror, old := heldMirror(t)
			if corruptHold {
				mirror.objects[HoldKey("dr", dbA, holdKeep)] = []byte(`{}`)
			} else {
				delete(mirror.objects, WALKey("dr", dbA, segName(2)+".00000028.backup"))
			}
			queueMirrorDeletes(t, store, "base/"+old.ID+".json", "base/"+old.ID+".tar.zst", "wal/"+segName(2))
			applied, err := offsite.RunOnce(t.Context())
			if applied != 0 || (err != nil) != corruptHold || !mirror.has(BaseKey("dr", dbA, old.ID+".tar.zst")) {
				t.Fatalf("unverified anchor replay = %d, %v", applied, err)
			}
			// Even an unreadable pin must not strand its authorized release
			// behind the recovery-file deletions it is protecting.
			if err := store.DeleteHold(t.Context(), dbA, holdKeep); err != nil {
				t.Fatal(err)
			}
			if applied, _ := offsite.RunOnce(t.Context()); applied != 1 {
				t.Fatalf("release behind deferred deletes applied %d entries", applied)
			}
			if applied, err := offsite.RunOnce(t.Context()); err != nil || applied != 3 {
				t.Fatalf("cleanup after release = %d, %v", applied, err)
			}
		})
	}
}

type blockedHoldListing struct {
	*memRemote
	started chan struct{}
}

func (r blockedHoldListing) List(ctx context.Context, prefix string) ([]string, error) {
	if strings.Contains(prefix, "/holds/") {
		close(r.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return r.memRemote.List(ctx, prefix)
}

func TestMirrorHoldCheckDoesNotBlockPrimaryUploads(t *testing.T) {
	store, uploader, _, bucket, mirror := mirrored(t)
	putFile(t, store.LocalStore, dbA, segName(2), "wal")
	started := make(chan struct{})
	store.Offsite = blockedHoldListing{mirror, started}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pruned := make(chan error, 1)
	go func() {
		_, err := store.PruneAll(ctx, t0.Add(3*time.Hour), time.Hour)
		pruned <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("pruner did not begin checking mirror holds")
	}
	uploaded := make(chan error, 1)
	go func() { _, err := uploader.RunOnce(t.Context()); uploaded <- err }()
	select {
	case err := <-uploaded:
		if err != nil || !bucket.has(WALKey("native", dbA, segName(2))) {
			t.Fatalf("primary upload = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled mirror hold lookup blocked the primary uploader")
	}
	cancel()
	if err := <-pruned; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled prune = %v", err)
	}
}

func TestExplicitDropRemovesMirroredHolds(t *testing.T) {
	store, _, offsite, _, mirror, _ := heldMirror(t)
	if err := store.Drop(t.Context(), dbA); err != nil {
		t.Fatal(err)
	}
	if _, err := offsite.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if keys, err := mirror.List(t.Context(), "dr/"+dbA+"/"); err != nil || len(keys) != 0 {
		t.Fatalf("explicit drop left mirrored archive: %v, %v", keys, err)
	}
}
