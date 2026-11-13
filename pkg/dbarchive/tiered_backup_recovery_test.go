// adr: 1027
package dbarchive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestMirrorRestoresWhenPrimaryCatalogLacksTheRequestedPoint(t *testing.T) {
	for _, missing := range []string{"all", "older", "data", "manifest", "history", "error"} {
		t.Run(missing, func(t *testing.T) {
			store, uploader, offsite, bucket, mirror := mirrored(t)
			old := putBackup(t, store.LocalStore, dbA, 2, t0, true)
			putBackup(t, store.LocalStore, dbA, 5, t0.Add(2*time.Hour), true)
			putFile(t, store.LocalStore, dbA, segName(2), "wal-2")
			for _, pass := range []func(context.Context) (int, error){uploader.RunOnce, offsite.RunOnce} {
				if _, err := pass(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			want, err := store.OpenBackup(t.Context(), dbA, old.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantTar, err := io.ReadAll(want)
			_ = want.Close()
			if err != nil {
				t.Fatal(err)
			}
			for key := range bucket.objects {
				if missing == "all" || (missing == "older" && (strings.Contains(key, old.ID) || strings.Contains(key, segName(2)))) ||
					(missing == "data" && key == BaseKey("native", dbA, old.ID+".tar.zst")) ||
					(missing == "manifest" && key == BaseKey("native", dbA, old.ID+".json")) ||
					(missing == "history" && strings.HasSuffix(key, ".backup")) {
					delete(bucket.objects, key)
				}
			}
			cold := &TieredStore{LocalStore: newStore(t), Remote: bucket, Prefix: "native", Offsite: mirror, OffsitePrefix: "dr"}
			if missing == "error" {
				cold.Remote = downRemote{}
			}
			// Exercise the restore protocol, including history selection, the
			// complete decompressed backup, and its required WAL from a lost node.
			dial := serveServer(t, &Server{Store: cold, Backups: cold})
			var got bytes.Buffer
			id, err := dial(Header{DatabaseID: dbB, RestoreSource: dbA}).GetBackup(t0.Add(30*time.Minute), &got)
			if err != nil || id != old.ID || !bytes.Equal(got.Bytes(), wantTar) {
				t.Fatalf("restore = %q (%d bytes), %v", id, got.Len(), err)
			}
			var wal bytes.Buffer
			if err := dial(Header{DatabaseID: dbB, RestoreSource: dbA}).GetWAL(segName(2), &wal); err != nil || wal.String() != "wal-2" {
				t.Fatalf("WAL = %q, %v", wal.String(), err)
			}
		})
	}
}

func TestPrimaryRestoreDoesNotRequireMirrorCatalog(t *testing.T) {
	store, uploader, _, _, _ := mirrored(t)
	backup := putBackup(t, store.LocalStore, dbA, 2, t0, true)
	if _, err := uploader.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	mirror := &countedMirrorCatalog{}
	cold := &TieredStore{LocalStore: newStore(t), Remote: store.Remote, Prefix: "native", Offsite: mirror, OffsitePrefix: "dr"}
	if got, err := cold.SelectBackup(t.Context(), cold, dbA, t0.Add(time.Hour)); err != nil || got.ID != backup.ID {
		t.Fatalf("healthy primary restore = %+v, %v", got, err)
	}
	if mirror.calls != 0 {
		t.Fatal("healthy primary restore queried the mirror catalog")
	}
}

type countedMirrorCatalog struct {
	downRemote
	calls int
}

func (r *countedMirrorCatalog) List(context.Context, string) ([]string, error) {
	r.calls++
	return nil, errBucketGone
}

func TestMirrorSelectionRequiresPairedBackupAndConsistentStopTime(t *testing.T) {
	store, uploader, offsite, bucket, mirror := mirrored(t)
	backup := putBackup(t, store.LocalStore, dbA, 2, t0, true)
	for _, pass := range []func(context.Context) (int, error){uploader.RunOnce, offsite.RunOnce} {
		if _, err := pass(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	bucket.objects = map[string][]byte{}
	cold := &TieredStore{LocalStore: newStore(t), Remote: bucket, Prefix: "native", Offsite: mirror, OffsitePrefix: "dr"}
	if _, err := cold.SelectBackup(t.Context(), cold, dbA, t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("backup before stop time selected: %v", err)
	}
	delete(mirror.objects, BaseKey("dr", dbA, backup.ID+".tar.zst"))
	if _, err := cold.SelectBackup(t.Context(), cold, dbA, t0.Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("manifest without data selected: %v", err)
	}
}

type stalledBackupRemote struct {
	*memRemote
	started chan struct{}
	stopped chan struct{}
	// With ignoreCancel, only closing the pipe can unblock Get.
	ignoreCancel bool
}

func (r *stalledBackupRemote) Get(ctx context.Context, _ string, w io.Writer) error {
	defer close(r.stopped)
	if _, err := w.Write([]byte{0x28}); err != nil {
		return err
	}
	close(r.started)
	if r.ignoreCancel {
		for {
			if _, err := w.Write([]byte{0}); err != nil {
				return err
			}
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestClosingRemoteBackupUnblocksPartialStream(t *testing.T) {
	for _, ignoreCancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "waiting-for-cancellation", true: "blocked-writer"}[ignoreCancel], func(t *testing.T) {
			r := &stalledBackupRemote{memRemote: &memRemote{objects: map[string][]byte{}}, started: make(chan struct{}), stopped: make(chan struct{}), ignoreCancel: ignoreCancel}
			store := &TieredStore{LocalStore: newStore(t), Remote: r, Prefix: "native"}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			backup, err := store.OpenBackup(ctx, dbA, "20261013T090000Z")
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-r.started:
			case <-time.After(2 * time.Second):
				t.Fatal("download did not begin")
			}
			done := make(chan error, 2)
			// Concurrent, repeated closes must share one decoder shutdown.
			for range 2 {
				go func() { done <- backup.Close() }()
			}
			for range 2 {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(2 * time.Second):
					cancel()
					t.Fatal("Close waited for the decoder before unblocking its input")
				}
			}
			select {
			case <-r.stopped:
			case <-time.After(2 * time.Second):
				t.Fatal("Close left the download running")
			}
			if ctx.Err() != nil {
				t.Fatal("Close cancelled its parent context")
			}
		})
	}
}
