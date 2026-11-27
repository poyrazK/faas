package imaged

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// fakeRetryClock is a settable clock for runtimeBaseRetries.
type fakeRetryClock struct{ t time.Time }

func (c *fakeRetryClock) now() time.Time { return c.t }

func newTestRuntimeBaseRetries() (*runtimeBaseRetries, *fakeRetryClock) {
	clock := &fakeRetryClock{t: time.Date(2026, 11, 27, 9, 1, 37, 0, time.UTC)}
	return &runtimeBaseRetries{now: clock.now, pending: map[string]runtimeBaseRetry{}}, clock
}

// spec: §4.6
// adr: 463 — a deferred stale-base preparation waits out its backoff, and
// fails once its node has not converged with the fleet within the window.
func TestRuntimeBaseRetries(t *testing.T) {
	r, clock := newTestRuntimeBaseRetries()
	if !r.due("dep") {
		t.Fatal("an unknown deployment must be due")
	}
	if !r.hold("dep") || r.due("dep") {
		t.Fatal("a first stale failure must be held back")
	}
	clock.t = clock.t.Add(runtimeBaseRetryEvery - time.Second)
	if r.due("dep") {
		t.Fatal("retried before the backoff elapsed")
	}
	clock.t = clock.t.Add(time.Second)
	if !r.due("dep") || !r.due("other") {
		t.Fatal("not due after the backoff")
	}
	clock.t = clock.t.Add(runtimeBaseRetryWindow)
	if r.hold("dep") || !r.due("dep") {
		t.Fatal("held past the retry window")
	}
	if !r.hold("dep") {
		t.Fatal("an expired deployment's next stale failure starts a new window")
	}
	r.forget("dep")
	if !r.due("dep") {
		t.Fatal("a forgotten deployment must be due")
	}
	var none *runtimeBaseRetries
	if none.hold("dep") || !none.due("dep") {
		t.Fatal("a nil tracker must defer nothing")
	}
	none.forget("dep")
}

func TestRuntimeBaseStagingError(t *testing.T) {
	stale := fmt.Errorf("imaged: stage runtime base node24: %w", state.ErrApplicationStandardRuntimeStale)
	if err := runtimeBaseStagingError(stale); !errors.Is(err, errRuntimeBaseStale) || !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatalf("stale staging = %v, want errRuntimeBaseStale wrapping the cause", err)
	}
	other := errors.New("pull runtime base manifest: 503")
	if err := runtimeBaseStagingError(other); errors.Is(err, errRuntimeBaseStale) || err.Error() != other.Error() {
		t.Fatalf("other staging error = %v, want it unchanged", err)
	}
}

// spec: §4.6
// adr: 463 — production, 2026-11-27: during a rollout, a node still on the
// previous release failed a deployment because the other release had moved
// the runtime base ("runtime inputs changed after admission"). The attempt now
// stays recoverable within the retry window and fails after it; any other
// error still fails the deployment.
func TestSettleImagePreparationDefersStaleRuntimeBase(t *testing.T) {
	stale := fmt.Errorf("imaged: ensure runtime base node24: %w", runtimeBaseStagingError(state.ErrApplicationStandardRuntimeStale))
	tests := []struct {
		name         string
		err          error
		pastWindow   bool
		wantDeferred bool
	}{
		{name: "stale runtime base", err: stale, wantDeferred: true},
		{name: "stale runtime base past the window", err: stale, pastWindow: true},
		{name: "other failure", err: errors.New("imaged: build app layer: disk full")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _, dep := imagePreparationFixture(t, "memory")
			ctx := context.Background()
			scans := 0
			h := imagePreparationHandler(t, store, &fakeBuilder{}, &fakeNotifier{}, &scans)
			retries, clock := newTestRuntimeBaseRetries()
			h.runtimeBaseRetries = retries
			if tt.pastWindow {
				retries.pending[dep.ID] = runtimeBaseRetry{first: clock.t.Add(-runtimeBaseRetryWindow), next: clock.t}
			}
			err := tt.err
			h.settleImagePreparation(ctx, dep.ID, &err)
			got, _ := store.DeploymentByID(ctx, dep.ID)
			if tt.wantDeferred {
				if !errors.Is(err, errImagePreparationRecovery) || got.Status.IsTerminal() || retries.due(dep.ID) {
					t.Fatalf("err=%v status=%s due=%v, want a recoverable preparation held back", err, got.Status, retries.due(dep.ID))
				}
				return
			}
			if errors.Is(err, errImagePreparationRecovery) || got.Status != state.DeployFailed || len(retries.pending) != 0 {
				t.Fatalf("err=%v status=%s pending=%d, want the deployment failed", err, got.Status, len(retries.pending))
			}
		})
	}
}

// spec: §4.6
// adr: 463 — the recovery loop resumes a deferred preparation only after its
// backoff, instead of re-staging the base on every 2 s tick.
func TestRecoverImageWorkWaitsOutRuntimeBaseBackoff(t *testing.T) {
	ctx := context.Background()
	store, app, dep := imagePreparationFixture(t, "memory")
	builder := &fakeBuilder{bytesOut: 9}
	scans := 0
	h := imagePreparationHandler(t, store, builder, &fakeNotifier{}, &scans)
	retries, clock := newTestRuntimeBaseRetries()
	h.runtimeBaseRetries = retries
	retries.hold(dep.ID)
	loop := &Loop{handler: h, log: silentLogger()}
	work := []state.BuildImageWork{{AppID: app.ID, DeploymentID: dep.ID}}

	loop.recoverImageWork(ctx, work, "resume")
	if len(builder.calls) != 0 {
		t.Fatalf("builds = %d, want the backoff to skip the resume", len(builder.calls))
	}
	clock.t = clock.t.Add(runtimeBaseRetryEvery)
	loop.recoverImageWork(ctx, work, "resume")
	got, _ := store.DeploymentByID(ctx, dep.ID)
	if len(builder.calls) != 1 || got.Status.IsTerminal() || len(retries.pending) != 0 {
		t.Fatalf("after the backoff: builds=%d status=%s pending=%d", len(builder.calls), got.Status, len(retries.pending))
	}
}
