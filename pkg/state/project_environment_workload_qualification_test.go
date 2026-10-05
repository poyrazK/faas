// adr: 590
package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type workloadQualificationTestStore interface {
	state.Store
	state.ProjectReleaseSetStore
	state.ProjectReleaseSetReader
	state.ProjectEnvironmentQualificationStore
	state.ProjectEnvironmentWorkloadQualificationStore
	state.ProjectEnvironmentWorkloadSpecStore
	state.ProjectEnvironmentPromotionReleaseSetStore
	state.ProjectPromotionDeploymentStore
}

func TestMemQualificationPinsWorkloadConfigurations(t *testing.T) {
	testQualificationPinsWorkloadConfigurations(t, state.NewMemStore())
}

func testQualificationPinsWorkloadConfigurations(t *testing.T, store workloadQualificationTestStore) {
	t.Helper()
	ctx := context.Background()
	account, project, releases := testProjectReleaseReadContract(t, store)
	active := releases[len(releases)-1]
	app, err := store.AppByID(ctx, active.Members[0].AppID)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.RAMMB, settings.StartCommand = 512, "serve tested"
	first, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "production", app.ID, 0, settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ProjectEnvironmentWorkloadConfigHashes(ctx, account.ID, project.ID, "production", active.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("legacy deployment qualifies new desired settings: %v", err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	active, err = store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: deployment.ID}})
	if err != nil {
		t.Fatal(err)
	}
	hashes, scoped, err := store.ProjectEnvironmentWorkloadConfigHashes(ctx, account.ID, project.ID, "production", active.ID)
	if err != nil || !scoped || hashes[app.Slug] != first.Hash {
		t.Fatalf("release fingerprints = %v, %v, %v", hashes, scoped, err)
	}
	ok := 204
	result := state.ProjectEnvironmentQualificationResult{WorkloadSlug: app.Slug, DeploymentID: deployment.ID, Status: "passed", HTTPStatus: &ok}
	checks := []state.ProjectEnvironmentQualificationCheck{
		{Name: "health", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{result}},
		{Name: "smoke", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{result}},
	}
	secretHash, err := api.ProjectEnvironmentSecretRevisionHash(nil)
	if err != nil {
		t.Fatal(err)
	}
	create := func(fingerprints map[string]string) (state.ProjectEnvironmentQualification, error) {
		return store.CreateProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", active.ID, 0,
			api.EmptyProjectEnvironmentConfigHash(), map[string]string{app.Slug: secretHash}, checks, fingerprints)
	}
	if _, err := create(nil); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("missing workload fingerprints accepted for scoped settings: %v", err)
	}
	receipt, err := create(hashes)
	if err != nil || receipt.WorkloadConfigHashes[app.Slug] != first.Hash {
		t.Fatalf("qualification = %+v, %v", receipt, err)
	}
	receipt.WorkloadConfigHashes[app.Slug] = "mutated"
	hashes[app.Slug] = "caller-mutated"
	latest, err := store.LatestProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", active.ID)
	if err != nil || latest.WorkloadConfigHashes[app.Slug] != first.Hash {
		t.Fatalf("receipt was mutable: %+v, %v", latest, err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	previous, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	targetRelease, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "staging", 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: previous.ID}})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage,
		TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLiveDark(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	promotion, _, err := store.CreateProjectEnvironmentPromotion(ctx, state.ProjectEnvironmentPromotion{
		AccountID: account.ID, ProjectID: project.ID, ProjectSlug: project.Slug,
		FromEnvironment: "production", ToEnvironment: "staging", Status: "running",
		PromotionHash: first.Hash, IdempotencyKey: "qualification-workload-fence",
		ReleaseGraphMode: true, SourceReleaseSetID: active.ID, SourceQualificationID: latest.ID,
		PreviousTargetReleaseSetID: targetRelease.ID, ReleaseTTLSeconds: 1800,
	}, []state.ProjectEnvironmentPromotionWorkload{{WorkloadSlug: app.Slug, WorkloadName: app.WorkloadName,
		SourceDeploymentID: deployment.ID, PreviousTargetDeploymentID: previous.ID, Status: "pending"}})
	if err != nil {
		t.Fatal(err)
	}
	settings.RAMMB = 1024
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "production", app.ID, first.Revision, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := create(latest.WorkloadConfigHashes); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("edit during probes accepted: %v", err)
	}
	if _, err := store.PublishProjectEnvironmentPromotionReleaseSet(ctx, account.ID, promotion.ID, 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: candidate.ID}}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed source published after qualification: %v", err)
	}
	stillActive, err := store.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "staging")
	if err != nil || stillActive.ID != targetRelease.ID {
		t.Fatalf("rejected publication changed target graph: %+v, %v", stillActive, err)
	}
}
