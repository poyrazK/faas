package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// SnapshotCaptureBudget is the wall-clock budget for a park or prime
// capture of an instance whose app layer is layerBytes long.
//
// SnapshotBudgetFor sizes the budget by guest memory, which was all a
// capture wrote when it was set. Since ADR-633 a capture also freezes the
// instance's private drive, a clone of its app layer, and publishes it
// through the storage backend. With FAAS_STORAGE_DRIVE_DELTA off (the
// default) that upload is the whole layer, zstd-compressed, in the same RPC.
// On production-us (OVH S3, 2026-11-27) publishing a 555 MB layer took 24 s
// of a Rails prime's 44.7 s capture against a 45 s budget, and the same app
// with Thruster in front of Puma, a 770 MB Ruby image and the 1.2 GB Umami
// image ran out of it on every attempt, so their deployments failed although
// each app passed its health check within seconds. The layer is therefore
// budgeted at the same per-GB rate as memory. layerBytes <= 0 (unknown)
// adds nothing.
func SnapshotCaptureBudget(ramMB int, layerBytes int64) time.Duration {
	budget := SnapshotBudgetFor(ramMB)
	if layerBytes <= 0 {
		return budget
	}
	return budget + time.Duration(float64(SnapshotBudgetPerGB)*(float64(layerBytes)/float64(1<<30)))
}

// snapshotCaptureTimeoutError marks a snapshot capture that ran out of its
// own budget, as opposed to the cold boot before it. markPrimeFailed names
// the capture budget instead of the cold-boot one, which sent customers to
// raise startup_deadline_s for an app that had started in seconds.
type snapshotCaptureTimeoutError struct {
	budget time.Duration
	err    error
}

func (e *snapshotCaptureTimeoutError) Error() string {
	return fmt.Sprintf("snapshot capture exceeded its %s budget: %v", e.budget, e.err)
}

func (e *snapshotCaptureTimeoutError) Unwrap() error { return e.err }

// captureBudgetExpired reports whether a capture run under captureCtx failed
// because that context's own deadline passed while the caller's ctx is
// still live (a daemon shutdown is not a capture timeout).
func captureBudgetExpired(ctx, captureCtx context.Context, err error) bool {
	return err != nil && ctx.Err() == nil && errors.Is(captureCtx.Err(), context.DeadlineExceeded)
}

// snapshotCaptureBudget is the capture budget the engine gave a SNAPSHOTTING
// row, for the watchdog's reprieve. A deployment lookup error falls back to
// the memory-only budget.
func (w *Watchdog) snapshotCaptureBudget(ctx context.Context, ins state.Instance) time.Duration {
	if ins.DeploymentID == "" {
		return SnapshotBudgetFor(ins.RAMMB)
	}
	dep, err := w.store.DeploymentByID(ctx, ins.DeploymentID)
	if err != nil {
		return SnapshotBudgetFor(ins.RAMMB)
	}
	return SnapshotCaptureBudget(ins.RAMMB, dep.RootfsBytes)
}

type finalPrimeAttemptKey struct{}

// withFinalPrimeAttempt marks the last prime attempt dispatchPrime makes.
func withFinalPrimeAttempt(ctx context.Context) context.Context {
	return context.WithValue(ctx, finalPrimeAttemptKey{}, true)
}

// finalPrimeAttempt reports whether no prime retry follows this attempt.
// A capture failure the retry would otherwise absorb then activates the
// deployment without a snapshot (ADR-005), instead of failing a release
// whose app booted healthy.
func finalPrimeAttempt(ctx context.Context) bool {
	final, _ := ctx.Value(finalPrimeAttemptKey{}).(bool)
	return final
}
