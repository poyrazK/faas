//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261124212701: gatewayd reads an app's edge-routed flags from its
// project environment's latest configuration, and only those flags.
func TestPg_FeatureFlagEdgeForApp(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, appID, _ := seedLiveDeploy(t, s, ctx)

	if b, err := s.FeatureFlagEdgeForApp(ctx, appID, ""); err != nil || b.Version != 0 || len(b.Flags) != 0 {
		t.Fatalf("app outside a project: %+v %v", b, err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct, Slug: "flags-edge", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET project_id = $1 WHERE id = $2`, project.ID, appID); err != nil {
		t.Fatalf("attach app to project: %v", err)
	}
	env, err := s.ProjectEnvironmentBySlug(ctx, acct, project.ID, "production")
	if err != nil {
		t.Fatalf("production environment: %v", err)
	}
	scope := state.FeatureFlagScope{AccountID: acct, ProjectID: project.ID, EnvironmentID: env.ID}
	variants := []flags.FlagVariant{{Key: "a", Weight: 5000}, {Key: "b", Weight: 5000}}
	plain := flags.Flag{Key: "plain", Type: "variant", Enabled: true, Default: "a", Variants: variants}
	if _, err := s.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: 0, Config: flags.Config{Flags: []flags.Flag{plain}}, Actor: acct}); err != nil {
		t.Fatalf("UpdateFeatureFlags: %v", err)
	}
	if b, err := s.FeatureFlagEdgeForApp(ctx, appID, "production"); err != nil || len(b.Flags) != 0 {
		t.Fatalf("no edge routes: %+v %v", b, err)
	}

	stored, err := s.GetFeatureFlags(ctx, scope, 0)
	if err != nil {
		t.Fatal(err)
	}
	routed := plain
	routed.Key, routed.Edge = "pricing", &flags.Edge{Routes: []flags.EdgeRoute{{Path: "/pricing", Variants: map[string]flags.EdgeAction{"b": {Rewrite: "/pricing-b"}}}}}
	next := stored.Config
	next.Flags = append(next.Flags, routed)
	if _, err := s.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: 1, Config: next, Actor: acct}); err != nil {
		t.Fatalf("UpdateFeatureFlags: %v", err)
	}
	for _, environment := range []string{"", "default", "production"} {
		b, err := s.FeatureFlagEdgeForApp(ctx, appID, environment)
		if err != nil || b.Version != 2 || b.EnvironmentID != env.ID || len(b.Flags) != 1 || b.Flags[0].Key != "pricing" ||
			b.Flags[0].Edge.Routes[0].Variants["b"].Rewrite != "/pricing-b" {
			t.Fatalf("edge routes for %q = %+v, %v; want only pricing at version 2", environment, b, err)
		}
	}
	if b, err := s.FeatureFlagEdgeForApp(ctx, appID, "staging"); err != nil || len(b.Flags) != 0 {
		t.Fatalf("another environment: %+v %v", b, err)
	}
	if b, err := s.FeatureFlagEdgeForApp(ctx, "not-a-uuid", ""); err != nil || len(b.Flags) != 0 {
		t.Fatalf("invalid id: %+v %v", b, err)
	}
}
