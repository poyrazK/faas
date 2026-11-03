package apidserver_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedAbortedCanary stores a canary candidate aborted before it completed,
// created at, and superseded as a completed release retires it.
func seedAbortedCanary(t *testing.T, store *state.MemStore, appID string, at time.Time) state.Deployment {
	t.Helper()
	aborted, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: appID, ImageDigest: "sha256:" + apidserver.Repeat("a", 64), Kind: state.DeploymentKindImage,
		Status: state.DeploySuperseded, CreatedAt: at, CanaryTotalSteps: 4, RolloutState: "aborted",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.DeploymentByID(context.Background(), aborted.ID)
	if err != nil || got.Status != state.DeploySuperseded || got.RolloutState != "aborted" || got.RolloutCompletedAt != nil {
		t.Fatalf("fixture: aborted canary = %+v (%v)", got, err)
	}
	return got
}

// Release review 2026-10-28: a canary aborted between the predecessor and the
// release is retired as superseded when the release completes. The
// post-release watch took the newest superseded row, so it compared the
// release with the aborted canary and rolled back to it. It now returns where
// a plain rollback would: the release that served before.
func TestPostReleaseWatchSkipsAnAbortedCanary(t *testing.T) {
	f := newPostReleaseFixture(t, "prw-aborted", true, time.Minute)
	aborted := seedAbortedCanary(t, *f.e.Store(), f.release.AppID, f.release.CreatedAt.Add(-time.Millisecond))
	f.telemetry.windows[f.release.ID] = canary.HealthWindow{Requests: 300, ServerErrors: 1, P95LatencyMS: 700}
	f.sweep(t)
	var decision api.RolloutDecision
	if err := json.Unmarshal(f.read(t, f.release.ID).RolloutDecision, &decision); err != nil || decision.StableDeploymentID != f.previous.ID {
		t.Fatalf("decision = %+v (%v), want the comparison and rollback against %s", decision, err, f.previous.ID)
	}
	if prev := f.read(t, f.previous.ID); prev.Status != state.DeploySnapshotting {
		t.Fatalf("predecessor status = %s, want the readiness-gated %s", prev.Status, state.DeploySnapshotting)
	}
	if got := f.read(t, aborted.ID); got.Status != state.DeploySuperseded {
		t.Fatalf("aborted canary status = %s, want it left superseded", got.Status)
	}
}

// The first-wake 5xx guard had the same rule and the same fix.
func TestRollbackOn5xxSkipsAnAbortedCanary(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	previous := apidserver.MustSeedDeployment(t, e, "r5-aborted")
	if err := (*e.Store()).MarkDeploymentLive(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	app, _ := (*e.Store()).AppBySlug(ctx, "r5-aborted")
	aborted := seedAbortedCanary(t, *e.Store(), app.ID, time.Now().UTC().Add(500*time.Millisecond))
	release, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:" + apidserver.Repeat("f", 64), Kind: state.DeploymentKindImage,
		Status: state.DeployBuilding, CreatedAt: time.Now().UTC().Add(time.Second), RollbackOn5xx: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkDeploymentLive(ctx, release.ID); err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkDeploymentSuperseded(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	store := &rollbackOn5xxTestStore{MemStore: (*e.Store()), requests: 40, serverErrors: 2}
	(*(*e.S()).Store()) = store
	for _, errors := range []int64{2, 38} { // healthy first wake, then failing
		store.serverErrors = errors
		if err := (*e.S()).RollbackOn5xxSweep(ctx, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := store.DeploymentByID(ctx, previous.ID); got.Status != state.DeploySnapshotting {
		t.Fatalf("predecessor status = %s, want the readiness-gated %s", got.Status, state.DeploySnapshotting)
	}
	if got, _ := store.DeploymentByID(ctx, aborted.ID); got.Status != state.DeploySuperseded {
		t.Fatalf("aborted canary status = %s, want it left superseded", got.Status)
	}
}
