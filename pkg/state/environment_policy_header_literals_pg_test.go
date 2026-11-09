//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261108110000 — amendment 2026-11-09: the backfill lists environment
// policies storing a credential as written, leaves out an environment with an
// active Git source for the app (its values come from the repository), and
// replaces a policy only if it is unchanged since it was read.
func TestPgEnvironmentPolicyHeaderLiterals(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "policy-literals@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop", RepoFullName: "example/shop", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "api", Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 1, Manifest: state.AppManifest{Port: 8080}})
	if err != nil {
		t.Fatal(err)
	}
	literal := []state.ProjectEnvironmentEdgeRule{{Kind: state.EdgeRuleKindHeaders, MatchPath: "/", Priority: 100, Enabled: true,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
			RequestHeaders: []state.EdgeRuleHeaderOp{{Name: "Authorization", Action: "set", Value: "Bearer env"}},
		}}}}
	policy, err := store.PutProjectEnvironmentEdgePolicy(ctx, state.ProjectEnvironmentEdgePolicy{AccountID: account.ID, ProjectID: project.ID,
		AppID: app.ID, EnvironmentSlug: "production", Rules: literal})
	if err != nil {
		t.Fatal(err)
	}
	listed := func() *state.ProjectEnvironmentEdgePolicy {
		t.Helper()
		page, err := store.ListEnvironmentEdgePoliciesWithRequestHeaderLiterals(ctx, "", "", 1000)
		if err != nil {
			t.Fatal(err)
		}
		for i := range page {
			if page[i].AppID == app.ID {
				return &page[i]
			}
		}
		return nil
	}
	found := listed()
	if found == nil || found.Rules[0].Action.Headers.RequestHeaders[0].Value != "Bearer env" || !found.UpdatedAt.Equal(policy.UpdatedAt) {
		t.Fatalf("listed = %+v", found)
	}

	sealed := []state.ProjectEnvironmentEdgeRule{literal[0]}
	sealed[0].Action = state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
		RequestHeaders: []state.EdgeRuleHeaderOp{{Name: "Authorization", Action: "set", Secret: true, SecretID: "11111111-1111-4111-8111-111111111111"}},
	}}
	stale := *found
	stale.UpdatedAt = stale.UpdatedAt.Add(-1)
	if ok, err := store.ReplaceEnvironmentEdgePolicyRules(ctx, stale, sealed); err != nil || ok {
		t.Fatalf("stale replace = %v, %v; want it refused", ok, err)
	}
	if ok, err := store.ReplaceEnvironmentEdgePolicyRules(ctx, *found, sealed); err != nil || !ok {
		t.Fatalf("replace = %v, %v", ok, err)
	}
	if listed() != nil {
		t.Fatal("a sealed policy is still listed")
	}

	// A literal again, now in an environment a Git source covers for the app.
	if _, err := store.PutProjectEnvironmentEdgePolicy(ctx, state.ProjectEnvironmentEdgePolicy{AccountID: account.ID, ProjectID: project.ID,
		AppID: app.ID, EnvironmentSlug: "production", Rules: literal}); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(ctx, account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{
		RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO environment_gitops_resources(source_id, logical_name, app_id) VALUES ($1, 'workload/api', $2)`, source.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	if got := listed(); got != nil {
		t.Fatalf("a Git-sourced policy was listed: %+v", got)
	}
}
