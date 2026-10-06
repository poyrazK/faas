// adr: 623 — a leased promotion commits its exact configuration and receipt together.
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingProjectPromotionMem(t *testing.T) {
	bindingProjectPromotionSuite(t, state.NewMemStore())
}

func bindingProjectPromotionSuite(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	a, err := store.CreateAccount(ctx, uuid.NewString()+"@checked-promotion.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "checked-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: a.ID, ProjectID: p.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "promotion-" + uuid.NewString()[:8], WorkloadName: "api", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 2, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	specs := store.(state.ProjectEnvironmentWorkloadSpecStore)
	preparedSettings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	targetHash, err := state.WorkloadSettingsHash(preparedSettings)
	if err != nil {
		t.Fatal(err)
	}
	preparedSettings.RAMMB = 256
	stageSpec, err := specs.PutProjectEnvironmentWorkloadSpec(ctx, a.ID, p.ID, "staging", app.ID, 0, preparedSettings)
	if err != nil {
		t.Fatal(err)
	}
	live := func(scope string) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, d.ID, "/image", "image/"+d.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		d, err = store.DeploymentByID(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	source, previous := live("staging"), live("production")
	releases := store.(state.ProjectReleaseSetStore)
	sourceGraph, err := releases.PublishProjectReleaseSet(ctx, a.ID, p.ID, "staging", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	previousGraph, err := releases.PublishProjectReleaseSet(ctx, a.ID, p.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: previous.ID}})
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := store.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(ctx, a.ID, app.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	promotion, workloads, err := store.CreateProjectEnvironmentPromotion(ctx, state.ProjectEnvironmentPromotion{AccountID: a.ID, ProjectID: p.ID, ProjectSlug: p.Slug, FromEnvironment: "staging", ToEnvironment: "production", Status: "running", IdempotencyKey: "checked-promotion", PromotionHash: stageSpec.Hash, ReleaseGraphMode: true, BindingsRequired: true, ReleaseTTLSeconds: 1800, SourceReleaseSetID: sourceGraph.ID, PreviousTargetReleaseSetID: previousGraph.ID, SyncConfig: true, SourceConfigHash: api.EmptyProjectEnvironmentConfigHash(), PreviousTargetConfigHash: api.EmptyProjectEnvironmentConfigHash(), SourceConfigSnapshot: json.RawMessage(`{}`), PreviousTargetConfigSnapshot: json.RawMessage(`{}`)}, []state.ProjectEnvironmentPromotionWorkload{{WorkloadSlug: app.Slug, WorkloadName: app.WorkloadName, SourceDeploymentID: source.ID, PreviousTargetDeploymentID: previous.ID, TargetDeploymentID: previous.ID, Status: "pending"}})
	if err != nil {
		t.Fatal(err)
	}
	worker := store.(state.BindingProjectPromotionStore)
	claim, err := worker.ClaimBindingProjectPromotion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.ClaimBindingProjectPromotion(ctx); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("concurrent lease accepted: %v", err)
	}
	id, err := worker.ReserveBindingProjectPromotionTarget(ctx, claim, workloads[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if id == previous.ID {
		t.Fatal("reservation reused the previous target")
	}
	again, err := worker.ReserveBindingProjectPromotionTarget(ctx, claim, workloads[0].ID)
	if err != nil || again != id {
		t.Fatalf("reservation not stable: %s %v", again, err)
	}
	stale := claim
	stale.Token = uuid.NewString()
	if _, err := worker.ReserveBindingProjectPromotionTarget(ctx, stale, workloads[0].ID); !errors.Is(err, state.ErrBindingProjectPromotionLease) {
		t.Fatalf("stale lease accepted: %v", err)
	}
	input := state.ProjectEnvironmentPromotionWorkloadSpecInput{PromotionID: promotion.ID, SourceDeploymentID: source.ID, SourceHash: stageSpec.Hash, PreviousTargetHash: targetHash}
	candidate := state.Deployment{ID: id, AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: source.ImageDigest, TrafficPercentExplicit: true, Reason: "environment promotion/" + promotion.ID}
	creator := store.(state.ProjectEnvironmentPromotionWorkloadSpecStore)
	if _, err := creator.CreateDeploymentForEnvironmentPromotion(ctx, candidate, input); !errors.Is(err, state.ErrBindingProjectPromotionLease) {
		t.Fatalf("unleased candidate accepted: %v", err)
	}
	leased := state.WithBindingProjectPromotionTarget(ctx, claim, id)
	d, err := creator.CreateDeploymentForEnvironmentPromotion(leased, candidate, input)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := creator.CreateDeploymentForEnvironmentPromotion(leased, candidate, input)
	if err != nil || retry.ID != d.ID {
		t.Fatalf("restart created a duplicate: %+v %v", retry, err)
	}
	if err := store.SetDeploymentRootfs(leased, id, "/image", "image/"+source.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.(state.ProjectPromotionDeploymentStore).MarkDeploymentLiveDark(leased, id); err != nil {
		t.Fatal(err)
	}
	if err := worker.CheckpointBindingProjectPromotionTarget(ctx, claim, workloads[0].ID); err != nil {
		t.Fatal(err)
	}
	members := []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: id}}
	report := api.ProjectReleaseCheckResponse{ProjectID: p.ID, Environment: "production", ExpectedActiveReleaseID: previousGraph.ID, TTLSeconds: 1800, Members: []api.ProjectReleaseSetMemberResponse{{AppID: app.ID, DeploymentID: id}}, Passed: true, CheckedAt: time.Now().UTC(), Checks: []api.BindingCheckReport{{App: app.Slug, Scope: "production", DeploymentID: id, Passed: true}}}
	report.GraphDigest = api.ProjectReleaseGraphDigest(report)
	unchanged := func() {
		t.Helper()
		active, err := store.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(ctx, a.ID, p.ID, "production")
		if err != nil || active.ID != previousGraph.ID {
			t.Fatalf("graph changed: %+v %v", active, err)
		}
		current, err := store.AppByID(ctx, app.ID)
		if err != nil || current.RAMMB != 128 {
			t.Fatalf("config changed: %+v %v", current, err)
		}
		saved, _, err := store.ProjectEnvironmentPromotionByID(ctx, a.ID, p.Slug, "production", promotion.ID)
		if err != nil || saved.Status != "running" || saved.TargetReleaseSetID != "" {
			t.Fatalf("receipt changed: %+v %v", saved, err)
		}
	}
	fences := graphFences(ctx, t, store, a, members)
	expired := append([]state.BindingPromotionFence(nil), fences...)
	expired[0].ValidUntil = time.Now().Add(-time.Second)
	if _, err := worker.PublishBindingCheckedEnvironmentPromotion(state.WithBindingReleaseFences(ctx, expired), claim, 1800, members, report); err == nil {
		t.Fatal("expired evidence accepted")
	}
	unchanged()
	if _, err := store.(state.ProjectEnvironmentPromotionReleaseSetStore).PublishProjectEnvironmentPromotionReleaseSet(ctx, a.ID, promotion.ID, 1800, members); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("unchecked activation: %v", err)
	}
	unchanged()
	if err := store.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.PublishBindingCheckedEnvironmentPromotion(state.WithBindingReleaseFences(ctx, fences), claim, 1800, members, report); err == nil {
		t.Fatal("preexisting drift rebased")
	}
	unchanged()
	fences = graphFences(ctx, t, store, a, members)
	activated, err := worker.PublishBindingCheckedEnvironmentPromotion(state.WithBindingReleaseFences(ctx, fences), claim, 1800, members, report)
	if err != nil {
		t.Fatal(err)
	}
	saved, rows, err := store.ProjectEnvironmentPromotionByID(ctx, a.ID, p.Slug, "production", promotion.ID)
	if err != nil || saved.Status != "succeeded" || saved.TargetReleaseSetID != activated.ID || saved.BindingWorkerToken != "" || len(saved.BindingsCheck) == 0 || rows[0].VerificationStatus != "verified" {
		t.Fatalf("atomic receipt: %+v %+v %v", saved, rows, err)
	}
	current, err := store.AppByID(ctx, app.ID)
	if err != nil || current.RAMMB != 256 {
		t.Fatalf("captured settings not applied: %+v %v", current, err)
	}
	if err := store.SetDeploymentRootfs(leased, id, "/late", "late", 1); !errors.Is(err, state.ErrBindingProjectPromotionLease) {
		t.Fatalf("late artifact publication accepted: %v", err)
	}
	report.Passed = false
	if err := worker.UpdateBindingProjectPromotionCheck(ctx, claim, report, "late blocker", false); !errors.Is(err, state.ErrBindingProjectPromotionLease) {
		t.Fatalf("late worker replaced receipt: %v", err)
	}
}
