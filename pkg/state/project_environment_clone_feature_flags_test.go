// adr: 585
package state_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneFlagFixtureStore interface {
	state.FeatureFlagStore
	CreatePlatformTenant(context.Context, string, string, string, int) (state.PlatformTenant, bool, error)
}

type cloneFlagTestStore interface {
	state.Store
	cloneFlagFixtureStore
	CloneProjectEnvironment(context.Context, state.ProjectEnvironmentClone, api.Limits) (state.ProjectEnvironment, state.ProjectEnvironmentCloneResult, error)
	RollbackProjectEnvironmentClone(context.Context, string, string, string) error
}

func cloneFlagFixture(t *testing.T, s cloneFlagFixtureStore, scope state.FeatureFlagScope) (state.FeatureFlagVersion, string) {
	t.Helper()
	tenant, _, err := s.CreatePlatformTenant(t.Context(), scope.AccountID, "flags-customer", "Flags customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	percentage := 2500
	config := flags.Config{Groups: map[string][]string{"early": {tenant.ID}}, Flags: []flags.Flag{
		{Key: "checkout", Enabled: true, Default: false, Rules: []flags.Rule{{ID: "ramp", Group: "early", Rollout: &percentage, Value: true,
			Progression: &flags.ProgressiveRollout{Stages: []int{2500, 10000}, MinimumUsedRequests: 10, MaximumP95LatencyMS: 1000, WindowSeconds: api.FlagsMinProgressiveWindowSeconds}}}},
		{Key: "theme", Type: "variant", Enabled: true, Default: "control", Variants: []flags.FlagVariant{{Key: "control", Weight: 3000}, {Key: "test", Weight: 7000}},
			Rules: []flags.Rule{{ID: "subject", Customers: []string{tenant.ID}, Rollout: &percentage, RolloutUnit: "subject"}}},
	}}
	first, err := s.UpdateFeatureFlags(t.Context(), state.FeatureFlagUpdate{Scope: scope, Config: config, Actor: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	first.Flags[0].Description = "captured-description"
	captured, err := s.UpdateFeatureFlags(t.Context(), state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: first.Version, Config: first.Config, Actor: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	return captured, tenant.ID
}

func TestMemProjectEnvironmentCloneFeatureFlagsIsolation(t *testing.T) {
	projectEnvironmentCloneFeatureFlagsIsolation(t, state.NewMemStore())
}

func projectEnvironmentCloneFeatureFlagsIsolation(t *testing.T, s cloneFlagTestStore) {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, "clone-flags@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "flag-clone"})
	if err != nil {
		t.Fatal(err)
	}
	prod, err := s.ProjectEnvironmentBySlug(ctx, a.ID, p.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	scope := state.FeatureFlagScope{AccountID: a.ID, ProjectID: p.ID, EnvironmentID: prod.ID}
	source, tenantID := cloneFlagFixture(t, s, scope)
	stage, _, err := s.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{AccountID: a.ID, ProjectID: p.ID, SourceSlug: "production", TargetSlug: "stage"}, api.MustLimitsFor(a.Plan))
	if err != nil {
		t.Fatal(err)
	}
	targetScope := scope
	targetScope.EnvironmentID = stage.ID
	target, err := s.GetFeatureFlags(ctx, targetScope, 0)
	if err != nil || target.Version != 1 || target.EnvironmentID != stage.ID || target.Actor != "environment-clone" || target.RestoredFrom != 0 || !reflect.DeepEqual(target.Config, source.Config) {
		t.Fatalf("isolated flag copy: version=%d, err=%v", target.Version, err)
	}
	history, err := s.ListFeatureFlagVersions(ctx, targetScope, 0)
	if err != nil || len(history) != 1 {
		t.Fatalf("source history leaked: %d, %v", len(history), err)
	}
	// Sample both customer and subject allocations with unchanged rollout seeds.
	for i := 0; i < 128; i++ {
		customer := uuid.NewString()
		if flags.Bucket(source.Flags[0].Seed, "checkout", customer) != flags.Bucket(target.Flags[0].Seed, "checkout", customer) ||
			flags.SubjectVariantBucket(source.Flags[1].Seed, "theme", tenantID, customer) != flags.SubjectVariantBucket(target.Flags[1].Seed, "theme", tenantID, customer) {
			t.Fatal("cloning changed rollout allocation")
		}
	}
	source.Flags[0].Description = "production-only"
	if _, err := s.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: source.Version, Config: source.Config, Actor: "developer"}); err != nil {
		t.Fatal(err)
	}
	target, err = s.GetFeatureFlags(ctx, targetScope, 0)
	if err != nil || target.Flags[0].Description != "captured-description" {
		t.Fatalf("production edit changed stage: %v", err)
	}
	target.Flags[0].Description = "stage-only"
	target.Flags = append(target.Flags, flags.Flag{Key: "new_stage_flag", Enabled: true, Default: false})
	edited, err := s.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: targetScope, ExpectedVersion: target.Version, Config: target.Config, Actor: "developer"})
	if err != nil || edited.Version != 2 || edited.Flags[0].Seed != source.Flags[0].Seed || edited.Flags[2].Seed != uuid.NewSHA1(uuid.MustParse(stage.ID), []byte("new_stage_flag")).String() {
		t.Fatalf("stage edit/new flag seed: %v", err)
	}
	unchanged, err := s.GetFeatureFlags(ctx, scope, 0)
	if err != nil || unchanged.Flags[0].Description != "production-only" || len(unchanged.Flags) != 2 {
		t.Fatalf("stage edit changed production: %v", err)
	}
	// Deletion/recreation must not retain the previous environment's history.
	if err := s.RollbackProjectEnvironmentClone(ctx, a.ID, p.ID, "stage"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFeatureFlags(ctx, targetScope, 0); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted flag scope survived: %v", err)
	}
	recreated, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: a.ID, ProjectID: p.ID, Slug: "stage"})
	if err != nil {
		t.Fatal(err)
	}
	targetScope.EnvironmentID = recreated.ID
	clear, err := s.GetFeatureFlags(ctx, targetScope, 0)
	if err != nil || clear.Version != 0 || len(clear.Flags) != 0 {
		t.Fatalf("recreated environment inherited flags: %v", err)
	}
}

func TestMemProjectEnvironmentCloneFeatureFlagsPublication(t *testing.T) {
	for _, fault := range []string{"before_flags", "after_flags", "after_flags_same_config"} {
		t.Run(fault, func(t *testing.T) {
			projectEnvironmentClonePublicationContract(t, state.NewMemStore(), false, fault)
		})
	}
}
