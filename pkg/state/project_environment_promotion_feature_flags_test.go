// adr: 569
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

type promotionFlagTestStore interface {
	cloneFlagTestStore
	state.ProjectReleaseSetStore
	state.ProjectReleaseSetReader
	state.ProjectEnvironmentPromotionReleaseSetStore
	state.ProjectEnvironmentPromotionFeatureFlagStore
	state.ProjectPromotionDeploymentStore
}

type promotionFlagFixture struct {
	store          promotionFlagTestStore
	account        state.Account
	project        state.Project
	source, target state.FeatureFlagScope
	sourceFlags    state.FeatureFlagVersion
	targetFlags    state.FeatureFlagVersion
	previous       state.ProjectReleaseSet
	candidate      []state.ProjectReleaseMember
	promotion      state.ProjectEnvironmentPromotion
}

func newPromotionFlagFixture(t *testing.T, store promotionFlagTestStore, mode string) promotionFlagFixture {
	t.Helper()
	ctx := t.Context()
	a, err := store.CreateAccount(ctx, "promotion-flags-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "promotion-flags"})
	if err != nil {
		t.Fatal(err)
	}
	prod, err := store.ProjectEnvironmentBySlug(ctx, a.ID, p.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	f := promotionFlagFixture{store: store, account: a, project: p,
		target: state.FeatureFlagScope{AccountID: a.ID, ProjectID: p.ID, EnvironmentID: prod.ID}}
	if mode != "unpublished" && mode != "first_source_publish" {
		f.targetFlags, _ = cloneFlagFixture(t, store, f.target)
	} else {
		f.targetFlags, err = store.GetFeatureFlags(ctx, f.target, 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	var stage state.ProjectEnvironment
	if mode == "first_source_publish" {
		stage, err = store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: a.ID, ProjectID: p.ID, Slug: "staging"})
	} else {
		stage, _, err = store.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{AccountID: a.ID, ProjectID: p.ID,
			SourceSlug: "production", TargetSlug: "staging"}, api.MustLimitsFor(a.Plan))
	}
	if err != nil {
		t.Fatal(err)
	}
	f.source = f.target
	f.source.EnvironmentID = stage.ID
	f.sourceFlags, err = store.GetFeatureFlags(ctx, f.source, 0)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "unpublished" && mode != "first_source_publish" {
		config := f.sourceFlags.Config
		if mode == "empty" {
			config = flags.Config{}
		} else {
			config.Flags[0].Default = true
			config.Flags = append(config.Flags, flags.Flag{Key: "stage_new", Enabled: true, Default: true})
		}
		f.sourceFlags, err = store.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: f.source,
			ExpectedVersion: f.sourceFlags.Version, Config: config, Actor: "developer"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if mode == "seed_conflict" {
		f.targetFlags.Config.Flags = append(f.targetFlags.Config.Flags, flags.Flag{Key: "stage_new", Enabled: true, Default: true})
		f.targetFlags, err = store.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: f.target, ExpectedVersion: f.targetFlags.Version,
			Config: f.targetFlags.Config, Actor: "developer"})
		if err != nil {
			t.Fatal(err)
		}
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "flag-api", WorkloadName: "api", Status: state.AppActive,
		Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	createLive := func(environment string) state.Deployment {
		t.Helper()
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: environment, Kind: state.DeploymentKindImage,
			TrafficPercentExplicit: true, ImageDigest: "sha256:" + uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLiveDark(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		return dep
	}
	previous := createLive("production")
	f.previous, err = store.PublishProjectReleaseSet(ctx, a.ID, p.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: previous.ID}})
	if err != nil {
		t.Fatal(err)
	}
	candidate := createLive("production")
	f.candidate = []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: candidate.ID}}
	source := createLive("staging")
	sourceGraph, err := store.PublishProjectReleaseSet(ctx, a.ID, p.ID, "staging", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	input := state.ProjectEnvironmentPromotion{AccountID: a.ID, ProjectID: p.ID, ProjectSlug: p.Slug,
		FromEnvironment: "staging", ToEnvironment: "production", Status: "running", ReleaseGraphMode: true, ReleaseTTLSeconds: 1800,
		SourceReleaseSetID: sourceGraph.ID, PreviousTargetReleaseSetID: f.previous.ID, PromotionHash: api.EmptyProjectEnvironmentConfigHash(),
		IdempotencyKey: "flag-promotion", SyncConfig: mode != "artifact_only", SourceConfigHash: api.EmptyProjectEnvironmentConfigHash(),
		PreviousTargetConfigHash: api.EmptyProjectEnvironmentConfigHash(), SourceConfigSnapshot: json.RawMessage(`{}`), PreviousTargetConfigSnapshot: json.RawMessage(`{}`)}
	input.SourceFeatureFlagsHash, err = state.FeatureFlagsPromotionHash(f.sourceFlags)
	if err != nil {
		t.Fatal(err)
	}
	input.PreviousTargetFeatureFlagsHash, err = state.FeatureFlagsPromotionHash(f.targetFlags)
	if err != nil {
		t.Fatal(err)
	}
	stale := input
	stale.SourceFeatureFlagsHash = api.EmptyProjectEnvironmentConfigHash()
	if input.SyncConfig {
		if _, _, err := store.CreateProjectEnvironmentPromotion(ctx, stale, nil); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("stale preview started promotion: %v", err)
		}
	}
	f.promotion, _, err = store.CreateProjectEnvironmentPromotion(ctx, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f promotionFlagFixture) publish(ctx context.Context) (state.ProjectReleaseSet, error) {
	return f.store.PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidate)
}

func (f promotionFlagFixture) rollback(ctx context.Context) (state.ProjectReleaseSet, error) {
	return f.store.RollbackProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.previous.Members)
}

func TestMemPromotionFeatureFlags(t *testing.T) {
	for _, fault := range promotionFlagCases {
		t.Run(fault, func(t *testing.T) { testPromotionFeatureFlags(t, state.NewMemStore(), fault) })
	}
}

var promotionFlagCases = []string{"success", "empty", "unpublished", "artifact_only", "source_edit", "target_edit", "after_cutover", "after_rollback", "bad_graph", "first_source_publish", "seed_conflict"}

func testPromotionFeatureFlags(t *testing.T, store promotionFlagTestStore, fault string) {
	t.Helper()
	ctx := t.Context()
	f := newPromotionFlagFixture(t, store, fault)
	update := func(scope state.FeatureFlagScope) state.FeatureFlagVersion {
		t.Helper()
		current, err := store.GetFeatureFlags(ctx, scope, 0)
		if err != nil {
			t.Fatal(err)
		}
		next, err := store.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: current.Version, Config: current.Config, Actor: "external"})
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	assertUnchanged := func(graphID string, expected state.FeatureFlagVersion) {
		t.Helper()
		graph, err := store.ActiveProjectReleaseSet(ctx, f.account.ID, f.project.ID, "production")
		if err != nil || graph.ID != graphID {
			t.Fatalf("rejected operation changed graph: %s, %v", graph.ID, err)
		}
		current, err := store.GetFeatureFlags(ctx, f.target, 0)
		if err != nil || current.Version != expected.Version || !reflect.DeepEqual(current.Config, expected.Config) {
			t.Fatalf("rejected operation changed flags: version %d, %v", current.Version, err)
		}
	}
	if fault == "source_edit" || fault == "target_edit" || fault == "bad_graph" || fault == "first_source_publish" || fault == "seed_conflict" {
		expected := f.targetFlags
		switch fault {
		case "source_edit":
			update(f.source)
		case "first_source_publish":
			if _, err := store.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: f.source,
				Config: flags.Config{Flags: []flags.Flag{{Key: "first_flag", Enabled: true, Default: true}}}, Actor: "developer"}); err != nil {
				t.Fatal(err)
			}
		case "target_edit":
			expected = update(f.target)
		case "bad_graph":
			f.candidate[0].DeploymentID = uuid.NewString()
		}
		if _, err := f.publish(ctx); err == nil {
			t.Fatal("stale or invalid promotion published")
		}
		assertUnchanged(f.previous.ID, expected)
		if receipt, err := store.ProjectEnvironmentPromotionFeatureFlags(ctx, f.account.ID, f.promotion.ID); err != nil || receipt.TargetVersion != 0 {
			t.Fatalf("rejected promotion retained an activation receipt: %+v, %v", receipt, err)
		}
		return
	}
	activated, err := f.publish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.GetFeatureFlags(ctx, f.target, 0)
	if err != nil {
		t.Fatal(err)
	}
	if fault == "artifact_only" {
		if current.Version != f.targetFlags.Version || !reflect.DeepEqual(current.Config, f.targetFlags.Config) {
			t.Fatal("artifact-only promotion changed flags")
		}
	} else {
		if current.Version != f.targetFlags.Version+1 || current.Actor != "environment-promotion" || current.RestoredFrom != 0 || !reflect.DeepEqual(current.Config, f.sourceFlags.Config) {
			t.Fatalf("flag activation did not preserve tested config/seeds: version %d, actor %s", current.Version, current.Actor)
		}
		receipt, err := store.ProjectEnvironmentPromotionFeatureFlags(ctx, f.account.ID, f.promotion.ID)
		if err != nil || receipt.TargetVersion != current.Version || receipt.RollbackVersion != 0 {
			t.Fatalf("activation receipt: %+v, %v", receipt, err)
		}
	}
	if fault == "after_cutover" {
		expected := update(f.target)
		if _, err := f.publish(ctx); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("publication replay accepted later identical flags: %v", err)
		}
		if _, err := f.rollback(ctx); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("rollback overwrote later identical flags: %v", err)
		}
		assertUnchanged(activated.ID, expected)
		return
	}
	// A changed source after committed cutover cannot alter replay or rollback.
	update(f.source)
	if retry, err := f.publish(ctx); err != nil || retry.ID != activated.ID {
		t.Fatalf("publication replay: %s, %v", retry.ID, err)
	}
	restored, err := f.rollback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := store.GetFeatureFlags(ctx, f.target, 0)
	if err != nil || !reflect.DeepEqual(rolledBack.Config, f.targetFlags.Config) {
		t.Fatalf("rollback did not restore previous flag config: %v", err)
	}
	if fault != "artifact_only" {
		if rolledBack.Version != current.Version+1 || rolledBack.Actor != "environment-promotion-rollback" || rolledBack.RestoredFrom != f.targetFlags.Version {
			t.Fatalf("rollback version/history identity: %+v", rolledBack)
		}
		receipt, err := store.ProjectEnvironmentPromotionFeatureFlags(ctx, f.account.ID, f.promotion.ID)
		if err != nil || receipt.RollbackVersion != rolledBack.Version {
			t.Fatalf("rollback receipt: %+v, %v", receipt, err)
		}
	}
	if fault == "after_rollback" {
		expected := update(f.target)
		if _, err := f.rollback(ctx); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("rollback replay accepted later identical flags: %v", err)
		}
		assertUnchanged(restored.ID, expected)
		return
	}
	if retry, err := f.rollback(ctx); err != nil || retry.ID != restored.ID {
		t.Fatalf("rollback replay: %s, %v", retry.ID, err)
	}
}
