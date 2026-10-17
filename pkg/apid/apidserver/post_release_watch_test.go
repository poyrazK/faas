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

// postReleaseTelemetry stands in for Postgres request telemetry, per
// deployment.
type postReleaseTelemetry struct {
	windows map[string]canary.HealthWindow
	reads   int
}

func (f *postReleaseTelemetry) RequestTelemetryCircuitBreakerSummary(_ context.Context, _, deploymentID string, _, _ time.Time) (int64, int64, float64, int64, float64, int64, int64, error) {
	f.reads++
	w := f.windows[deploymentID]
	return w.Requests, w.ServerErrors, w.P95LatencyMS, w.ColdBootRequests, w.ColdBootP95LatencyMS, 0, 0, nil
}

type postReleaseFixture struct {
	e                 apidserver.TestEnv
	previous, release state.Deployment
	telemetry         *postReleaseTelemetry
}

func newPostReleaseFixture(t *testing.T, slug string, optIn bool, completedAgo time.Duration) postReleaseFixture {
	t.Helper()
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	store := *e.Store()
	previous := apidserver.MustSeedDeployment(t, e, slug)
	if err := store.MarkDeploymentLive(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	app, err := store.AppBySlug(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	release, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:" + apidserver.Repeat("e", 64), Kind: state.DeploymentKindImage,
		Status: state.DeployBuilding, CreatedAt: time.Now().UTC().Add(time.Second), RollbackOn5xx: optIn,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, release.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentSuperseded(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	completed := time.Now().UTC().Add(-completedAgo)
	started := completed.Add(-4 * time.Minute)
	if _, err := store.SafedeployStampRollout(ctx, release.ID, "complete", &started, &completed, nil, ""); err != nil {
		t.Fatal(err)
	}
	telemetry := &postReleaseTelemetry{windows: map[string]canary.HealthWindow{
		previous.ID: {Requests: 400, ServerErrors: 2, P95LatencyMS: 40},
		release.ID:  {Requests: 300, ServerErrors: 1, P95LatencyMS: 45},
	}}
	(*e.S()).SetPostReleaseTelemetry(telemetry)
	return postReleaseFixture{e: e, previous: previous, release: release, telemetry: telemetry}
}

func (f postReleaseFixture) sweep(t *testing.T) {
	t.Helper()
	if err := (*f.e.S()).PostReleaseWatchSweep(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func (f postReleaseFixture) read(t *testing.T, id string) state.Deployment {
	t.Helper()
	d, err := (*f.e.Store()).DeploymentByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A release that only slows down at full traffic passed its canary; the
// watch compares it with its predecessor's traffic before the rollout and
// rolls it back with the evidence on the release.
func TestPostReleaseWatchRollsBackARegressedRelease(t *testing.T) {
	f := newPostReleaseFixture(t, "prw-slow", true, time.Minute)
	f.sweep(t)
	if got := f.read(t, f.release.ID); got.Status != state.DeployLive || got.LastAutoRollbackReason != "" {
		t.Fatalf("healthy release = %s %q, want it left live", got.Status, got.LastAutoRollbackReason)
	}
	if f.telemetry.reads == 0 {
		t.Fatal("a completed release inside the window was not evaluated")
	}

	f.telemetry.windows[f.release.ID] = canary.HealthWindow{Requests: 300, ServerErrors: 1, P95LatencyMS: 700}
	f.sweep(t)
	got := f.read(t, f.release.ID)
	if got.LastAutoRollbackReason != string(state.AutoRollbackReasonPostReleaseRegression) {
		t.Fatalf("regressed release reason = %q, want %q", got.LastAutoRollbackReason, state.AutoRollbackReasonPostReleaseRegression)
	}
	var decision api.RolloutDecision
	if err := json.Unmarshal(got.RolloutDecision, &decision); err != nil {
		t.Fatalf("decode decision %s: %v", got.RolloutDecision, err)
	}
	if decision.Rule != api.RolloutRuleAbortPostReleaseP95 || decision.StableDeploymentID != f.previous.ID ||
		decision.Candidate == nil || decision.Candidate.P95LatencyMS != 700 || decision.Stable.P95LatencyMS != 40 {
		t.Fatalf("decision = %+v, want a p95 abort against the predecessor", decision)
	}
	if prev := f.read(t, f.previous.ID); prev.Status != state.DeploySnapshotting {
		t.Fatalf("predecessor status = %s, want the readiness-gated %s", prev.Status, state.DeploySnapshotting)
	}
	reads := f.telemetry.reads
	f.sweep(t)
	if f.telemetry.reads != reads {
		t.Fatal("a rolled-back release was evaluated again")
	}
}

// Releases that opted out of automatic rollback, and releases whose watch
// window has passed, are never judged.
func TestPostReleaseWatchSkipsOptOutsAndClosedWindows(t *testing.T) {
	for _, tc := range []struct {
		name         string
		optIn        bool
		completedAgo time.Duration
	}{
		{"opted out of automatic rollback", false, time.Minute},
		{"watch window passed", true, api.PostReleaseWatchWindow + api.RollbackOn5xxTelemetryGrace + time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPostReleaseFixture(t, "prw-skip", tc.optIn, tc.completedAgo)
			f.telemetry.windows[f.release.ID] = canary.HealthWindow{Requests: 300, ServerErrors: 200, P95LatencyMS: 900}
			f.sweep(t)
			if f.telemetry.reads != 0 {
				t.Fatalf("telemetry read %d times, want the release not watched", f.telemetry.reads)
			}
			if got := f.read(t, f.release.ID); got.Status != state.DeployLive || got.LastAutoRollbackReason != "" {
				t.Fatalf("release = %s %q, want untouched", got.Status, got.LastAutoRollbackReason)
			}
		})
	}
}
