// adr: 1027 — durable hold publication and independent mirror uploads.
package dbarchive

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

type holdPublicationRemote struct {
	Remote
	key              string
	entered, release chan struct{}
}

func (r *holdPublicationRemote) Put(ctx context.Context, key string, body io.ReadSeeker, size int64) error {
	if key == r.key {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.Remote.Put(ctx, key, body, size)
}

func TestOffsiteHoldPublicationSurvivesConcurrentReplay(t *testing.T) {
	for _, racing := range []bool{false, true} {
		name := "control"
		if racing {
			name = "replay_during_put"
		}
		t.Run(name, func(t *testing.T) {
			store, uploader, offsite, bucket, mirror := mirrored(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			store.now = func() time.Time { return t0.Add(30 * time.Minute) }
			old := putBackup(t, store.LocalStore, dbA, 2, t0, true)
			putFile(t, store.LocalStore, dbA, segName(2), "old-wal")
			if _, err := uploader.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := offsite.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			blocked := &holdPublicationRemote{Remote: bucket, key: HoldKey("native", dbA, holdKeep), entered: make(chan struct{}), release: make(chan struct{})}
			store.Remote = blocked
			offsite.Bucket = blocked
			done := make(chan error, 1)
			point := t0.Add(10 * time.Minute)
			go func() { _, err := store.PutHold(ctx, dbA, holdKeep, point); done <- err }()
			select {
			case <-blocked.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if racing {
				// Journal is visible, but the primary PUT is still in flight.
				if _, err := offsite.RunOnce(ctx); err != nil {
					t.Logf("replay deferred: %v", err)
				}
			}
			close(blocked.release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if _, err := store.PutHold(ctx, dbA, holdKeep, point); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if _, err := offsite.RunOnce(ctx); err != nil {
					t.Fatal(err)
				}
			}
			pending, _ := offsite.pending()
			t.Logf("primary hold=%v mirror hold=%v pending=%d", bucket.has(blocked.key), mirror.has(HoldKey("dr", dbA, holdKeep)), pending)
			if !mirror.has(HoldKey("dr", dbA, holdKeep)) {
				t.Error("successful hold placement permanently lost its mirror journal entry")
			}
			// A newer backup becomes the ordinary retention anchor. The old
			// snapshot must survive loss of only the primary bucket's hold metadata.
			putBackup(t, store.LocalStore, dbA, 5, t0.Add(time.Hour), true)
			putFile(t, store.LocalStore, dbA, segName(5), "new-wal")
			if _, err := uploader.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := offsite.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if err := bucket.Delete(ctx, blocked.key); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Prune(ctx, dbA, t0.Add(90*time.Minute)); err != nil {
				t.Fatal(err)
			}
			if _, err := offsite.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			present := mirror.has(BaseKey("dr", dbA, old.ID+".tar.zst"))
			t.Logf("held snapshot backup survived primary metadata loss and prune: %v", present)
			if !present {
				t.Fatal("a successfully held snapshot lost its off-site base backup")
			}
		})
	}
}

func TestOffsiteUploadFailureDoesNotBlockOtherDatabases(t *testing.T) {
	store, _, _, bucket, _ := mirrored(t)
	backup := putBackup(t, store.LocalStore, dbA, 2, t0, true)
	putFile(t, store.LocalStore, dbB, segName(3), "other-database-wal")
	mirror := &failingDatabaseRemote{memRemote: &memRemote{objects: map[string][]byte{}}, failKey: BaseKey("dr", dbA, backup.ID+".tar.zst")}
	offsite := NewOffsite(store.LocalStore, bucket, "native", mirror, "dr")
	for pass := 1; pass <= 2; pass++ {
		_, err := offsite.RunOnce(t.Context())
		if !errors.Is(err, errBucketGone) {
			t.Fatalf("expected isolated object failure, got %v", err)
		}
		if !mirror.has(WALKey("dr", dbB, segName(3))) {
			t.Errorf("pass %d: one database's failed backup blocked another database's mirror WAL", pass)
		}
		pending, _ := offsite.pending()
		if mirror.has(BaseKey("dr", dbA, backup.ID+".json")) || pending != 2 {
			t.Fatalf("manifest preceded its backup, or pending work was lost: %d", pending)
		}
	}
	mirror.failKey = ""
	if _, err := offsite.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !mirror.has(WALKey("dr", dbB, segName(3))) {
		t.Fatal("recovery did not mirror unrelated WAL")
	}
	if pending, _ := offsite.pending(); pending != 0 || !mirror.has(BaseKey("dr", dbA, backup.ID+".json")) {
		t.Fatalf("recovery did not finish backup upload: pending=%d", pending)
	}
}

func TestOffsiteHoldRetryRepairsMissingMirrorPin(t *testing.T) {
	store, _, offsite, _, mirror := mirrored(t)
	first, err := store.PutHold(t.Context(), dbA, holdKeep, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := offsite.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := mirror.Delete(t.Context(), HoldKey("dr", dbA, holdKeep)); err != nil {
		t.Fatal(err)
	}
	retry, err := store.PutHold(t.Context(), dbA, holdKeep, t0)
	if err != nil || retry != first {
		t.Fatalf("retry changed hold: %+v err=%v", retry, err)
	}
	if _, err := offsite.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !mirror.has(HoldKey("dr", dbA, holdKeep)) {
		t.Fatal("retry did not repair mirror pin")
	}
}

func TestOffsiteJournalRejectsInvalidHoldPayload(t *testing.T) {
	valid := offsiteOp{Op: offsiteCopy, Database: dbA, Path: "holds/" + holdKeep,
		Hold: &Hold{Name: holdKeep, PointInTime: t0, CreatedAt: t0}}
	if !valid.valid() {
		t.Fatal("valid pin rejected")
	}
	for _, change := range []func(*offsiteOp){
		func(op *offsiteOp) { op.Op = offsiteDelete },
		func(op *offsiteOp) { op.Path = "wal/" + segName(2) },
		func(op *offsiteOp) { op.Hold.Name = "another" },
		func(op *offsiteOp) { op.Hold.PointInTime = time.Time{} },
	} {
		op := valid
		hold := *valid.Hold
		op.Hold = &hold
		change(&op)
		if op.valid() {
			t.Fatalf("invalid payload accepted: %+v", op)
		}
	}
}
