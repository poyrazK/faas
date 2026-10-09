//go:build linux

// Modeled native ownership plus real anonymous verified copies; no VM load.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

type nativeRestoreBackingFixtureBackend struct {
	*nativeWritableImageFixture
	inputs []*os.File
	after  func(int) error
}

func (b *nativeRestoreBackingFixtureBackend) PrepareRestoreBacking(ctx context.Context, owner nativeLaunchRecord, root string, file *os.File, image nativeSnapshotBackingImage) (nativeImagePreparation, error) {
	if err := errors.Join(requireNativeSealedRestoreDescriptor(file, image.LogicalBytes), verifyNativeRestoreDigest(ctx, file, image.LogicalBytes, image.SHA256)); err != nil {
		return nil, err
	}
	b.inputs = append(b.inputs, file)
	if b.after != nil {
		if err := b.after(len(b.inputs)); err != nil {
			return nil, err
		}
	}
	return b.PrepareWritable(ctx, owner, root, "", image.Name)
}

func nativeRestoreBackingsFixture(t *testing.T) (*nativeCaptureSequenceFixture, nativeSnapshotBackingRecord, nativeQualificationRestoreRecord, nativeLaunchRecord, *nativeRestoreBackingFixtureBackend, [2]string, context.Context) {
	t.Helper()
	f := nativeCaptureSequence(t)
	if err := os.Chmod(f.directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := f.v.captureNativeSnapshotBackings(f.ctx, f.owner.Lease, f.backing); err != nil {
		t.Fatal(err)
	}
	completed := f.capture
	completed.Info, completed.Backing, completed.CompletedAt, completed.FCVersion = SnapshotInfo{MemBytes: 1, VMStateBytes: 1, StoredBytes: 1}, f.backing, time.Now(), "1.7.0"
	if err := f.q.writeCapture(f.incoming, completed); err != nil {
		t.Fatal(err)
	}
	record, err := f.q.readBackings(completed)
	if err != nil {
		t.Fatal(err)
	}
	var paths [2]string
	for i, image := range record.Images {
		paths[i] = filepath.Join(f.directory, "candidate-"+image.Name)
		if err := os.WriteFile(paths[i], []byte("captured-backing-"+image.Name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.lock.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.revoke(t.Context(), f.incoming.Execution); err != nil {
		t.Fatal(err)
	}
	retired := retireNativeImageFixtureOwner(t, f.j, f.owner)
	if err := f.j.retireAll(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
	if err := f.q.owner.confirmResourcesRemoved(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
	frame := f.incoming.Execution
	frame.InstanceID, frame.WakeID, frame.CleanupToken, frame.CaptureInstanceID = uuid.NewString(), uuid.NewString(), uuid.NewString(), frame.InstanceID
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	target, err := f.q.restores().claim(ctx, frame, "1.7.0")
	if err != nil {
		t.Fatal(err)
	}
	ctx = nativeQualificationRestoreContext(ctx, target)
	if err := f.q.owner.prepare(ctx, qualificationLease(frame.InstanceID)); err != nil {
		t.Fatal(err)
	}
	owner, err := f.q.owner.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	f.v.nativeRecovery.remember(owner)
	b := &nativeRestoreBackingFixtureBackend{nativeWritableImageFixture: f.b.nativeWritableImageFixture}
	f.v.nativeRecovery.imageSources, f.q.owner.imageSources = b, b
	return f, record, target, owner, b, paths, ctx
}

func TestNativeQualificationRestoreBackingsVerifyBothImagesBeforeTargetEffects(t *testing.T) {
	for _, change := range []string{"original", "base_digest", "kernel_size", "swapped", "symlink", "relative", "directory", "missing_record", "changed_record", "source_epoch", "source_reference", "generic", "recovered", "revoked", "canceled"} {
		t.Run(change, func(t *testing.T) {
			f, record, target, owner, b, paths, ctx := nativeRestoreBackingsFixture(t)
			switch change {
			case "base_digest":
				if err := os.WriteFile(paths[1], []byte("changed!"+"-backing-"+record.Images[1].Name), 0o600); err != nil {
					t.Fatal(err)
				}
			case "kernel_size":
				if err := os.Truncate(paths[0], 1); err != nil {
					t.Fatal(err)
				}
			case "swapped":
				paths[0], paths[1] = paths[1], paths[0]
			case "symlink":
				link := filepath.Join(f.directory, "symlink")
				if err := os.Symlink(paths[1], link); err != nil {
					t.Fatal(err)
				}
				paths[1] = link
			case "relative":
				paths[0] = "./candidate"
			case "directory":
				paths[1] = f.directory
			case "missing_record":
				path, _ := f.q.backingPath(f.capture.InstanceID)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "changed_record":
				path, _ := f.q.backingPath(f.capture.InstanceID)
				record.Images[1].Name = "changed-base.ext4"
				if err := writeNativeJournalValue(path, record); err != nil {
					t.Fatal(err)
				}
			case "source_epoch", "source_reference":
				sources, err := f.j.records()
				if err != nil {
					t.Fatal(err)
				}
				for _, source := range sources {
					if source.Epoch == record.Images[1].Epoch {
						if change == "source_epoch" {
							source.Identity.Inode++
						} else {
							source.References[0].Name += "-changed"
						}
						if err := writeNativeJournalValue(f.j.path(source), source); err != nil {
							t.Fatal(err)
						}
					}
				}
			case "generic":
				ctx = t.Context()
			case "recovered":
				delete(f.v.nativeRecovery.owned, owner.Lease.Instance)
			case "revoked":
				if _, err := f.q.restores().revoke(t.Context(), target.Execution); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			err := f.v.stageNativeQualificationRestoreBackings(ctx, owner, paths)
			if change != "original" {
				if err == nil || len(b.inputs) != 0 {
					t.Fatal("changed original reached target image effects", change, err, len(b.inputs))
				}
				return
			}
			if err != nil || len(b.inputs) != 2 {
				t.Fatal("original backing copies did not reach exact target", err, len(b.inputs))
			}
			for _, file := range b.inputs {
				if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatal("sealed backing input escaped target operation", err)
				}
			}
			if err := f.v.stageNativeQualificationRestoreBackings(ctx, owner, paths); err == nil || len(b.inputs) != 2 {
				t.Fatal("backing staging replayed original epochs", err)
			}
		})
	}
}

func TestNativeQualificationRestoreBackingCopiesSurviveCandidatePathChanges(t *testing.T) {
	f, _, _, owner, b, paths, ctx := nativeRestoreBackingsFixture(t)
	b.after = func(i int) error {
		if i == 1 {
			return os.WriteFile(paths[1], []byte("replacement must not reach the second epoch"), 0o600)
		}
		return nil
	}
	if err := f.v.stageNativeQualificationRestoreBackings(ctx, owner, paths); err != nil || len(b.inputs) != 2 {
		t.Fatal("target re-resolved a changed candidate", err)
	}
}

func TestNativeQualificationRestorePartialBackingEpochCannotReplay(t *testing.T) {
	f, _, _, owner, b, paths, ctx := nativeRestoreBackingsFixture(t)
	b.after = func(i int) error {
		if i == 2 {
			return errors.New("modeled lost second preparation acknowledgement")
		}
		return nil
	}
	if err := f.v.stageNativeQualificationRestoreBackings(ctx, owner, paths); err == nil || len(b.inputs) != 2 {
		t.Fatal("partial backing preparation promoted success", err)
	}
	b.after = nil
	if err := f.v.stageNativeQualificationRestoreBackings(ctx, owner, paths); err == nil || len(b.inputs) != 2 {
		t.Fatal("partial backing preparation acquired new native epochs", err)
	}
}
