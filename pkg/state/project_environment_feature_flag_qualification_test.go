// adr: 569
package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

type flagQualificationTestStore interface {
	workloadQualificationTestStore
	state.FeatureFlagStore
}

func TestMemQualificationPinsFeatureFlags(t *testing.T) {
	qualificationPinsFeatureFlags(t, state.NewMemStore())
}

func qualificationPinsFeatureFlags(t *testing.T, store flagQualificationTestStore, change ...func(context.Context, state.FeatureFlagScope, state.FeatureFlagVersion) error) {
	t.Helper()
	ctx := t.Context()
	account, project, releases := testProjectReleaseReadContract(t, store)
	source := releases[len(releases)-1]
	app, err := store.AppByID(ctx, source.Members[0].AppID)
	if err != nil {
		t.Fatal(err)
	}
	env, err := store.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	scope := state.FeatureFlagScope{AccountID: account.ID, ProjectID: project.ID, EnvironmentID: env.ID}
	ok := 204
	result := state.ProjectEnvironmentQualificationResult{WorkloadSlug: app.Slug, DeploymentID: source.Members[0].DeploymentID, Status: "passed", HTTPStatus: &ok}
	checks := []state.ProjectEnvironmentQualificationCheck{{Name: "health", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{result}},
		{Name: "smoke", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{result}}}
	secretHash, err := api.ProjectEnvironmentSecretRevisionHash(nil)
	if err != nil {
		t.Fatal(err)
	}
	qualify := func(hashes map[string]string) (state.ProjectEnvironmentQualification, error) {
		return store.CreateProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", source.ID, 0,
			api.EmptyProjectEnvironmentConfigHash(), map[string]string{app.Slug: secretHash}, checks, hashes)
	}
	legacy, scoped, err := store.ProjectEnvironmentWorkloadConfigHashes(ctx, account.ID, project.ID, "production", source.ID)
	if err != nil || scoped {
		t.Fatalf("legacy identity: %v", err)
	}
	if _, err := qualify(nil); err != nil {
		t.Fatalf("unconfigured flags broke legacy qualification: %v", err)
	}
	version, err := store.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope,
		Config: flags.Config{Flags: []flags.Flag{{Key: "checkout", Enabled: true, Default: true}}}, Actor: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	hashes, scoped, err := store.ProjectEnvironmentWorkloadConfigHashes(ctx, account.ID, project.ID, "production", source.ID)
	if err != nil || !scoped || hashes[app.Slug] == legacy[app.Slug] {
		t.Fatalf("flags did not enter qualification fingerprint: %v", err)
	}
	for _, old := range []map[string]string{nil, legacy} {
		if _, err := qualify(old); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("legacy receipt certified configured flags: %v", err)
		}
	}
	receipt, err := qualify(hashes)
	if err != nil {
		t.Fatal(err)
	}
	// Build a target graph and intent before changing only the flag revision.
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	previous, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "staging", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: target.ID}})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLiveDark(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	promotion, _, err := store.CreateProjectEnvironmentPromotion(ctx, state.ProjectEnvironmentPromotion{AccountID: account.ID, ProjectID: project.ID, ProjectSlug: project.Slug,
		FromEnvironment: "production", ToEnvironment: "staging", PromotionHash: hashes[app.Slug], IdempotencyKey: "flag-qualified", Status: "running",
		ReleaseGraphMode: true, SourceReleaseSetID: source.ID, SourceQualificationID: receipt.ID, PreviousTargetReleaseSetID: previous.ID, ReleaseTTLSeconds: 1800},
		[]state.ProjectEnvironmentPromotionWorkload{{WorkloadSlug: app.Slug, WorkloadName: app.WorkloadName, SourceDeploymentID: source.Members[0].DeploymentID, PreviousTargetDeploymentID: target.ID, Status: "pending"}})
	if err != nil {
		t.Fatal(err)
	}
	// A new version with identical contents is a different observed revision.
	if len(change) > 0 {
		if err := change[0](ctx, scope, version); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err := store.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: version.Version, Config: version.Config, Actor: "developer"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := qualify(hashes); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("flag publish during probes accepted: %v", err)
	}
	if _, err := store.PublishProjectEnvironmentPromotionReleaseSet(ctx, account.ID, promotion.ID, 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: candidate.ID}}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("flag edit after qualification reached cutover: %v", err)
	}
	current, err := store.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "staging")
	if err != nil || current.ID != previous.ID {
		t.Fatalf("rejected qualification changed target graph: %v", err)
	}
	fresh, _, err := store.ProjectEnvironmentWorkloadConfigHashes(ctx, account.ID, project.ID, "production", source.ID)
	if err != nil || fresh[app.Slug] == hashes[app.Slug] {
		t.Fatalf("new flag revision retained old identity: %v", err)
	}
	if _, err := qualify(fresh); err != nil {
		t.Fatalf("fresh probes could not qualify: %v", err)
	}
	// This public metadata must disclose no flag configuration or targeting.
	identity, err := state.FeatureFlagsQualificationHash(version)
	if err != nil || !api.ValidProjectEnvironmentConfigHash(identity) {
		t.Fatalf("flag identity: %v", err)
	}
}
