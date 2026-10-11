//go:build linux

// adr: 568 — modeled staging ownership plus real receipt reads; no VM load.
package fcvm

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func nativeQualificationRestoreStagingFixture(t *testing.T) (nativeRestoreInputFixture, nativeQualificationRestoreRecord, nativeLaunchRecord, *nativeRestoreStagingFixtureBackend, context.Context) {
	t.Helper()
	f := nativeRestoreInputsFixture(t, nil)
	q, v := f.capture.q, f.capture.v
	if err := f.capture.lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := q.writeCapture(f.capture.incoming, f.completed); err != nil {
		t.Fatal(err)
	}
	if _, err := q.revoke(t.Context(), f.capture.incoming.Execution); err != nil {
		t.Fatal(err)
	}
	source, err := q.owner.read(f.completed.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	source.ExitConfirmed, source.ResourcesRemoved = true, true
	if err := q.owner.write(source); err != nil {
		t.Fatal(err)
	}
	frame := f.capture.incoming.Execution
	frame.InstanceID, frame.WakeID, frame.CleanupToken, frame.CaptureInstanceID = uuid.NewString(), uuid.NewString(), uuid.NewString(), frame.InstanceID
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	r, err := q.restores().claim(ctx, frame, "1.7.0")
	if err != nil {
		t.Fatal(err)
	}
	ctx = nativeQualificationRestoreContext(ctx, r)
	if err := q.owner.prepare(ctx, qualificationLease(frame.InstanceID)); err != nil {
		t.Fatal(err)
	}
	owner, err := q.owner.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	b := &nativeRestoreStagingFixtureBackend{nativeWritableImageFixture: f.capture.b.nativeWritableImageFixture}
	q.owner.imageSources, v.nativeRecovery.imageSources = b, b
	v.nativeRecovery.remember(owner)
	v.nativeRecovery.publications, v.nativeImageStagingRoot, v.storage = f.journal, f.root, f.backend
	return f, r, owner, b, ctx
}

func TestNativeQualificationRestoreStagingJoinsOriginalTargetAndReceipts(t *testing.T) {
	for _, change := range []string{"original", "generic", "capture_permit", "permit_frame", "permit_generation", "permit_deadline", "target_generation", "target_lease", "revoked", "expired", "recovered", "missing_receipts", "capture_changed", "canceled"} {
		t.Run(change, func(t *testing.T) {
			f, r, owner, b, ctx := nativeQualificationRestoreStagingFixture(t)
			v, q := f.capture.v, f.capture.q
			switch change {
			case "generic":
				ctx = t.Context()
			case "capture_permit":
				ctx = nativeQualificationContext(t.Context(), f.capture.incoming)
			case "permit_frame":
				r.Execution.WakeID = uuid.NewString()
				ctx = nativeQualificationRestoreContext(ctx, r)
			case "permit_generation":
				r.Generation = uuid.NewString()
				ctx = nativeQualificationRestoreContext(ctx, r)
			case "permit_deadline":
				r.Deadline = r.Deadline.Add(time.Second)
				ctx = nativeQualificationRestoreContext(ctx, r)
			case "target_generation":
				owner.Generation = uuid.NewString()
			case "target_lease":
				owner.Lease.MemoryMaxMiB++
			case "revoked":
				if _, err := q.restores().revoke(t.Context(), r.Execution); err != nil {
					t.Fatal(err)
				}
			case "expired":
				// Journal clocks normally use wall time; model expiry without sleep.
				bound, err := q.restores().read(r.Execution.InstanceID)
				if err != nil {
					t.Fatal(err)
				}
				bound.AcceptedAt, bound.Deadline = time.Now().Add(-2*time.Minute), time.Now().Add(-time.Minute)
				if err := q.restores().write(bound); err != nil {
					t.Fatal(err)
				}
			case "recovered":
				delete(v.nativeRecovery.owned, owner.Lease.Instance)
			case "missing_receipts":
				v.nativeRecovery.publications = nil
			case "capture_changed":
				completed := f.completed
				completed.Info.StoredBytes++
				writeTamperedNativeQualificationCapture(t, q, f.capture.incoming, completed)
			case "canceled":
				cancelCtx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelCtx
			}
			err := v.stageNativeQualificationRestore(ctx, owner)
			if change != "original" {
				if err == nil || b.prepares != 0 || f.backend.opened != 0 {
					t.Fatal("changed target reached receipt reads or image effects", err, b.prepares, f.backend.opened)
				}
				return
			}
			if err != nil || b.prepares != 3 || f.backend.opened != 4 || f.backend.closed != 4 {
				t.Fatal("original target did not join four receipt reads and three image epochs", err, b.prepares, f.backend.opened)
			}
			if err := v.stageNativeQualificationRestore(ctx, owner); err == nil || b.prepares != 3 {
				t.Fatal("staging replay reacquired an uncertain native image epoch", err, b.prepares)
			}
			cleanup, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			if _, err := q.restores().revoke(cleanup, r.Execution); err != nil {
				t.Fatal("synchronous staging did not release original incoming authority", err)
			}
		})
	}
}
