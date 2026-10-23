package apidserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261021152201 — a scheduled release builds now, waits live without
// traffic, and starts the rollout its deploy chose when its time comes,
// after rechecking freezes and approvals.

type scheduleFixture struct {
	e       apidserver.TestEnv
	app     state.App
	serving state.Deployment
}

func newScheduleFixture(t *testing.T, slug string) scheduleFixture {
	t.Helper()
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	serving := apidserver.MustSeedDeployment(t, e, slug)
	if err := store.MarkDeploymentLive(ctx, serving.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.StampSafeReleaseWorkerLease(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	app, err := store.AppBySlug(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	return scheduleFixture{e: e, app: app, serving: serving}
}

// parked creates a release that is live without traffic, as a scheduled
// deploy leaves it once its build finishes.
func (f scheduleFixture) parked(t *testing.T, digest byte) state.Deployment {
	t.Helper()
	ctx := context.Background()
	store := *f.e.Store()
	d, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: f.app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + apidserver.Repeat(string(digest), 64),
		Status: state.DeployPending, TrafficPercent: 0, TrafficPercentExplicit: true, CreatedAt: time.Now().UTC().Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	return d
}

func (f scheduleFixture) read(t *testing.T, id string) state.Deployment {
	t.Helper()
	d, err := (*f.e.Store()).DeploymentByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (f scheduleFixture) schedule(t *testing.T, id string) api.ScheduledRelease {
	t.Helper()
	var sr api.ScheduledRelease
	if rec := guardDo(t, f.e, http.MethodGet, "/v1/deployments/"+id+"/schedule", nil, &sr); rec.Code != http.StatusOK {
		t.Fatalf("get schedule = %d %s", rec.Code, rec.Body)
	}
	return sr
}

func TestScheduledDeployWaitsWithoutTrafficThenStartsItsCanary(t *testing.T) {
	f := newScheduleFixture(t, "sched-app")
	e := f.e
	at := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	var dep api.DeploymentResponse
	rec := guardDo(t, e, http.MethodPost, "/v1/apps/sched-app/deployments",
		api.CreateDeploymentRequest{Image: imageRef('a'), ReleaseAt: at.Format(time.RFC3339)}, &dep)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("scheduled deploy = %d %s", rec.Code, rec.Body)
	}
	if dep.TrafficPercent != 0 || dep.CanaryTotalSteps != 0 || dep.ScheduledRelease == nil ||
		dep.ScheduledRelease.State != api.ScheduledReleaseStatePending || dep.ScheduledRelease.ReleaseAt != at.Format(time.RFC3339) ||
		dep.ScheduledRelease.Canary == nil || dep.ScheduledRelease.Canary.Preset == "none" {
		t.Fatalf("scheduled deployment = %+v / %+v", dep, dep.ScheduledRelease)
	}
	store := *e.Store()
	if err := store.MarkDeploymentLive(context.Background(), dep.ID); err != nil {
		t.Fatal(err)
	}
	if parked := f.read(t, dep.ID); parked.Status != state.DeployLive || parked.TrafficPercent != 0 {
		t.Fatalf("built release = %s at %d%%, want live without traffic", parked.Status, parked.TrafficPercent)
	}

	// Nobody can give it traffic by hand while it waits.
	rec = e.Do(t, http.MethodPatch, "/v1/deployments/"+dep.ID+"/traffic", api.UpdateDeploymentTrafficRequest{TrafficPercent: 100}, nil)
	apidserver.AssertProblem(t, rec, http.StatusConflict, api.CodeReleaseScheduled)

	var list api.ScheduledReleaseListResponse
	if rec := guardDo(t, e, http.MethodGet, "/v1/scheduled-releases?app=sched-app", nil, &list); rec.Code != http.StatusOK ||
		len(list.Items) != 1 || list.Items[0].DeploymentID != dep.ID || list.Items[0].App != "sched-app" {
		t.Fatalf("list = %d %+v", rec.Code, list)
	}

	ctx := context.Background()
	(*e.S()).StartDueScheduledReleases(ctx, at.Add(-time.Minute))
	if early := f.read(t, dep.ID); early.TrafficPercent != 0 {
		t.Fatalf("release started early: %d%%", early.TrafficPercent)
	}
	(*e.S()).StartDueScheduledReleases(ctx, at.Add(time.Second))
	started := f.read(t, dep.ID)
	if started.TrafficPercent <= 0 || started.TrafficPercent >= 100 || started.CanaryTotalSteps < 2 ||
		state.NormalizeRolloutState(started.RolloutState) != "rolling_out" || started.RolloutStartedAt == nil {
		t.Fatalf("started release = %d%% steps %d %s", started.TrafficPercent, started.CanaryTotalSteps, started.RolloutState)
	}
	if serving := f.read(t, f.serving.ID); serving.TrafficPercent != 100-started.TrafficPercent {
		t.Fatalf("serving release at %d%%, want %d%%", serving.TrafficPercent, 100-started.TrafficPercent)
	}
	if sr := f.schedule(t, dep.ID); sr.State != api.ScheduledReleaseStateStarted || sr.StartedAt == "" {
		t.Fatalf("schedule after the start = %+v", sr)
	}
}

func TestScheduledReleaseIsBlockedByAFreezeCreatedAfterIt(t *testing.T) {
	f := newScheduleFixture(t, "sched-frozen")
	e := f.e
	parked := f.parked(t, 'b')
	at := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
	var sr api.ScheduledRelease
	if rec := guardDo(t, e, http.MethodPut, "/v1/deployments/"+parked.ID+"/schedule",
		api.ScheduleReleaseRequest{ReleaseAt: at.Format(time.RFC3339), Canary: &api.CanaryPresetSpec{Preset: "none"}}, &sr); rec.Code != http.StatusOK {
		t.Fatalf("schedule = %d %s", rec.Code, rec.Body)
	}
	if sr.State != api.ScheduledReleaseStatePending || sr.Canary == nil || sr.Canary.Preset != "none" {
		t.Fatalf("schedule = %+v", sr)
	}
	if rec := guardDo(t, e, http.MethodPost, "/v1/freezes", api.CreateReleaseFreezeRequest{App: "sched-frozen", Kind: "window", Reason: "incident 4711"}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create freeze = %d %s", rec.Code, rec.Body)
	}
	(*e.S()).StartDueScheduledReleases(context.Background(), at.Add(time.Second))
	if d := f.read(t, parked.ID); d.TrafficPercent != 0 {
		t.Fatalf("frozen release took %d%% traffic", d.TrafficPercent)
	}
	blocked := f.schedule(t, parked.ID)
	if blocked.State != api.ScheduledReleaseStateBlocked || blocked.OutcomeCode != api.CodeReleaseFrozen || !strings.Contains(blocked.OutcomeDetail, "incident 4711") {
		t.Fatalf("blocked schedule = %+v", blocked)
	}
	// The release still waits; scheduling it again is how it goes out.
	rec := e.Do(t, http.MethodDelete, "/v1/deployments/"+parked.ID+"/schedule", nil, nil)
	apidserver.AssertProblem(t, rec, http.StatusNotFound, api.CodeScheduledReleaseNotFound)
}

func TestScheduleValidationFreezePreviewAndCancel(t *testing.T) {
	f := newScheduleFixture(t, "sched-rules")
	e := f.e
	now := time.Now().UTC()
	for name, req := range map[string]api.CreateDeploymentRequest{
		"too far ahead":   {Image: imageRef('c'), ReleaseAt: now.Add(8 * 24 * time.Hour).Format(time.RFC3339)},
		"in the past":     {Image: imageRef('c'), ReleaseAt: now.Add(-time.Hour).Format(time.RFC3339)},
		"not a time":      {Image: imageRef('c'), ReleaseAt: "tomorrow"},
		"traffic too":     {Image: imageRef('c'), ReleaseAt: now.Add(time.Hour).Format(time.RFC3339), TrafficPercent: new(int)},
		"named scope":     {Image: imageRef('c'), ReleaseAt: now.Add(time.Hour).Format(time.RFC3339), Scope: "staging"},
		"notify, no time": {Image: imageRef('c'), ReleaseNotify: []string{"releases"}},
	} {
		if rec := e.Do(t, http.MethodPost, "/v1/apps/sched-rules/deployments", req, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s, want 400", name, rec.Code, rec.Body)
		}
	}

	// A freeze active at the release time refuses the schedule now, unless
	// it is overridden; the override then covers exactly that freeze.
	start := now.Add(2 * time.Hour)
	if rec := guardDo(t, e, http.MethodPost, "/v1/freezes", api.CreateReleaseFreezeRequest{App: "sched-rules", Kind: "window", Reason: "launch",
		StartsAt: start.Add(-time.Minute).Format(time.RFC3339), EndsAt: start.Add(time.Hour).Format(time.RFC3339)}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create freeze = %d %s", rec.Code, rec.Body)
	}
	release := start.Format(time.RFC3339)
	rec := e.Do(t, http.MethodPost, "/v1/apps/sched-rules/deployments", api.CreateDeploymentRequest{Image: imageRef('d'), ReleaseAt: release}, nil)
	apidserver.AssertProblem(t, rec, http.StatusConflict, api.CodeReleaseFrozen)
	if p := decodeProblem(t, rec.Body.Bytes()); !strings.HasPrefix(p.Detail, "At "+release) {
		t.Fatalf("frozen schedule detail = %q", p.Detail)
	}
	var dep api.DeploymentResponse
	if rec := guardDo(t, e, http.MethodPost, "/v1/apps/sched-rules/deployments",
		api.CreateDeploymentRequest{Image: imageRef('e'), ReleaseAt: release, OverrideFreeze: "launch night"}, &dep); rec.Code != http.StatusAccepted {
		t.Fatalf("overridden schedule = %d %s", rec.Code, rec.Body)
	}
	if dep.ScheduledRelease == nil || dep.ScheduledRelease.OverrideFreeze != "launch night" || len(dep.ScheduledRelease.OverriddenFreezes) != 1 {
		t.Fatalf("overridden schedule = %+v", dep.ScheduledRelease)
	}

	// A release that serves cannot be scheduled.
	rec = e.Do(t, http.MethodPut, "/v1/deployments/"+f.serving.ID+"/schedule", api.ScheduleReleaseRequest{ReleaseAt: now.Add(time.Hour).Format(time.RFC3339)}, nil)
	apidserver.AssertProblem(t, rec, http.StatusConflict, api.CodeReleaseNotParked)

	var cancelled api.ScheduledRelease
	if rec := guardDo(t, e, http.MethodDelete, "/v1/deployments/"+dep.ID+"/schedule", nil, &cancelled); rec.Code != http.StatusOK ||
		cancelled.State != api.ScheduledReleaseStateCancelled {
		t.Fatalf("cancel = %d %+v", rec.Code, cancelled)
	}
	rec = e.Do(t, http.MethodDelete, "/v1/deployments/"+dep.ID+"/schedule", nil, nil)
	apidserver.AssertProblem(t, rec, http.StatusNotFound, api.CodeScheduledReleaseNotFound)
}

func TestApprovalPolicyKeepsParkedReleasesBehindACanary(t *testing.T) {
	f := newScheduleFixture(t, "sched-approved")
	e := f.e
	if rec := e.Do(t, http.MethodPut, "/v1/apps/sched-approved/release-approval-policy", api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1}, nil); rec.Code != http.StatusOK {
		t.Fatalf("set policy = %d %s", rec.Code, rec.Body)
	}
	parked := f.parked(t, 'f')
	rec := e.Do(t, http.MethodPatch, "/v1/deployments/"+parked.ID+"/traffic", api.UpdateDeploymentTrafficRequest{TrafficPercent: 100}, nil)
	apidserver.AssertProblem(t, rec, http.StatusConflict, api.CodeReleaseApprovalCanaryRequired)
	at := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	rec = e.Do(t, http.MethodPut, "/v1/deployments/"+parked.ID+"/schedule",
		api.ScheduleReleaseRequest{ReleaseAt: at, Canary: &api.CanaryPresetSpec{Preset: "none"}}, nil)
	apidserver.AssertProblem(t, rec, http.StatusConflict, api.CodeReleaseApprovalCanaryRequired)
	var sr api.ScheduledRelease
	if rec := guardDo(t, e, http.MethodPut, "/v1/deployments/"+parked.ID+"/schedule", api.ScheduleReleaseRequest{ReleaseAt: at}, &sr); rec.Code != http.StatusOK ||
		sr.Canary == nil || sr.Canary.Preset == "none" {
		t.Fatalf("default canary schedule = %d %+v", rec.Code, sr)
	}
}
