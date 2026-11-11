package apidserver_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// rollbackOn5xxTestStore adds the Postgres-only request summary to MemStore.
type rollbackOn5xxTestStore struct {
	*state.MemStore
	requests, serverErrors int64
	reads                  int
	lastUntil              time.Time
}

func (s *rollbackOn5xxTestStore) RequestTelemetryCircuitBreakerSummary(_ context.Context, _, _ string, _, until time.Time) (int64, int64, float64, int64, float64, int64, int64, error) {
	s.reads++
	s.lastUntil = until
	return s.requests, s.serverErrors, 0, 0, 0, 0, 0, nil
}

// rollbackOn5xxSweepAt is past the release's first closed telemetry minute:
// the guard reads only minutes whose gateway buckets have shipped.
func rollbackOn5xxSweepAt() time.Time { return time.Now().UTC().Add(3 * time.Minute) }

func TestRollbackOn5xxBreached(t *testing.T) {
	for _, tc := range []struct {
		requests, serverErrors int64
		want                   bool
	}{
		{requests: 30, serverErrors: 30, want: true},
		{requests: 10, serverErrors: 5, want: true},
		{requests: 4, serverErrors: 4, want: false},     // too few to judge
		{requests: 1000, serverErrors: 10, want: false}, // one bad route on a busy release
		{requests: 0, serverErrors: 0, want: false},
	} {
		if got := apidserver.RollbackOn5xxBreached(tc.requests, tc.serverErrors); got != tc.want {
			t.Errorf("rollbackOn5xxBreached(%d, %d) = %v, want %v", tc.requests, tc.serverErrors, got, tc.want)
		}
	}
}

// production-us hunt #4 (H4-26): `deploy --rollback-on-5xx` stored the opt-in
// and nothing read it, so v8 answered 100% 500s and stayed live. The worker
// opens the first-wake window on first traffic, leaves a healthy release
// alone, and hands a failing one's predecessor to the readiness-gated rollback.
func TestRollbackOn5xxSweepRollsBackAFailingRelease(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	previous := apidserver.MustSeedDeployment(t, e, "r5-app")
	if err := (*e.Store()).MarkDeploymentLive(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	app, _ := (*e.Store()).AppBySlug(ctx, "r5-app")
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
	store := &rollbackOn5xxTestStore{MemStore: (*e.Store())}
	(*(*e.S()).Store()) = store
	read := func(id string) state.Deployment {
		t.Helper()
		d, err := (*e.Store()).DeploymentByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	sweep := func() {
		t.Helper()
		if err := (*e.S()).RollbackOn5xxSweep(ctx, rollbackOn5xxSweepAt()); err != nil {
			t.Fatal(err)
		}
	}

	sweep() // no traffic yet
	if got := read(release.ID); got.FirstWakeAt != nil || got.Status != state.DeployLive {
		t.Fatalf("no traffic: release = status %s first_wake %v, want live with no window", got.Status, got.FirstWakeAt)
	}

	store.requests, store.serverErrors = 40, 2
	sweep() // healthy traffic opens the window and changes nothing else
	if got := read(release.ID); got.FirstWakeAt == nil || got.First5xxWindowEndsAt == nil || got.Status != state.DeployLive || got.LastAutoRollbackReason != "" {
		t.Fatalf("healthy traffic: release = %+v, want live with an open window", got)
	}
	if got := read(previous.ID); got.Status != state.DeploySuperseded {
		t.Fatalf("healthy traffic moved the predecessor to %s", got.Status)
	}

	store.requests, store.serverErrors = 40, 38
	sweep()
	if got := read(release.ID); got.LastAutoRollbackReason != string(state.AutoRollbackReasonThresholdExceeded) {
		t.Fatalf("failing release reason = %q, want %q", got.LastAutoRollbackReason, state.AutoRollbackReasonThresholdExceeded)
	}
	if got := read(previous.ID); got.Status != state.DeploySnapshotting {
		t.Fatalf("predecessor status = %s, want the readiness-gated %s", got.Status, state.DeploySnapshotting)
	}

	reads := store.reads
	sweep()
	if store.reads != reads {
		t.Fatalf("a rolled-back release was evaluated again (%d -> %d telemetry reads)", reads, store.reads)
	}
}

// adr: 1445 amendment 2 — a first-wake breach the guard cannot roll back,
// because a release command ran after the predecessor last served, is
// recorded once on the release, like the post-release watch's (release review
// 2026-11-01); it used to be only logged, on every sweep.
func TestRollbackOn5xxRecordsABreachItCannotRollBack(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	mem := *e.Store()
	previous := apidserver.MustSeedDeployment(t, e, "r5-kept")
	if err := mem.MarkDeploymentLive(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	app, _ := mem.AppBySlug(ctx, "r5-kept")
	release, err := mem.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:" + apidserver.Repeat("f", 64), Kind: state.DeploymentKindImage,
		Status: state.DeployBuilding, CreatedAt: time.Now().UTC().Add(time.Second), RollbackOn5xx: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.MarkDeploymentLive(ctx, release.ID); err != nil {
		t.Fatal(err)
	}
	if err := mem.MarkDeploymentSuperseded(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	if err := mem.SetDeploymentRootfs(ctx, release.ID, "/tmp/r.ext4", "apps/r5-kept/"+release.ID+".ext4", 4096); err != nil {
		t.Fatal(err)
	}
	runRelease(ctx, t, mem, (*e.Acct()).ID, app.ID, release.ID)
	store := &rollbackOn5xxTestStore{MemStore: mem, requests: 40, serverErrors: 38}
	(*(*e.S()).Store()) = store
	sweep := func() {
		t.Helper()
		if err := (*e.S()).RollbackOn5xxSweep(ctx, rollbackOn5xxSweepAt()); err != nil {
			t.Fatal(err)
		}
	}

	sweep()
	got, err := mem.DeploymentByID(ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.DeployLive || got.LastAutoRollbackReason != "" {
		t.Fatalf("release = %s %q, want it kept serving", got.Status, got.LastAutoRollbackReason)
	}
	if prev, _ := mem.DeploymentByID(ctx, previous.ID); prev.Status != state.DeploySuperseded {
		t.Fatalf("predecessor status = %s, want no rollback to it", prev.Status)
	}
	var decision api.RolloutDecision
	if err := json.Unmarshal(got.RolloutDecision, &decision); err != nil {
		t.Fatalf("decode decision %s: %v", got.RolloutDecision, err)
	}
	if decision.Rule != api.RolloutRuleAbortPostRelease5xx || decision.RollbackSkipped != api.CodeRollbackCrossesRelease ||
		decision.Candidate == nil || decision.Candidate.ServerErrors != 38 {
		t.Fatalf("decision = %+v, want a 5xx breach whose rollback was skipped", decision)
	}
	reads := store.reads
	sweep()
	if store.reads != reads {
		t.Fatal("a breach whose rollback was skipped was judged again")
	}
}

// adr: 625 amendment 2026-11-10 — gateways ship a 5xx at once and a 2xx with
// its minute's bucket, so the open minute read 6 requests / 6 errors for a
// release failing 3% of them (prod 2026-11-09). The guard reads only closed
// minutes and does not judge a release before its first one.
func TestRollbackOn5xxSweepReadsOnlyClosedMinutes(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	previous := apidserver.MustSeedDeployment(t, e, "r5-closed")
	if err := (*e.Store()).MarkDeploymentLive(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	app, _ := (*e.Store()).AppBySlug(ctx, "r5-closed")
	release, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:" + apidserver.Repeat("f", 64), Kind: state.DeploymentKindImage,
		Status: state.DeployBuilding, CreatedAt: time.Now().UTC(), RollbackOn5xx: true,
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
	created, err := (*e.Store()).DeploymentByID(ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	store := &rollbackOn5xxTestStore{MemStore: (*e.Store()), requests: 6, serverErrors: 6}
	(*(*e.S()).Store()) = store

	if err := (*e.S()).RollbackOn5xxSweep(ctx, created.CreatedAt.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if store.reads != 0 {
		t.Fatalf("judged the release from its open first minute (%d reads)", store.reads)
	}
	if got, _ := (*e.Store()).DeploymentByID(ctx, release.ID); got.LastAutoRollbackReason != "" || got.FirstWakeAt != nil {
		t.Fatalf("release = %+v, want untouched before its first closed minute", got)
	}

	now := created.CreatedAt.Add(150 * time.Second)
	if err := (*e.S()).RollbackOn5xxSweep(ctx, now); err != nil {
		t.Fatal(err)
	}
	if store.reads != 1 {
		t.Fatalf("telemetry reads = %d, want 1", store.reads)
	}
	if want := now.UTC().Add(-api.RouteHealthIngestionLag).Truncate(time.Minute); !store.lastUntil.Equal(want) {
		t.Fatalf("read until %s, want the last closed minute %s", store.lastUntil, want)
	}
}
