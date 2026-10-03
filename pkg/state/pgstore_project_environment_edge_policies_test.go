//go:build !no_pg

// adr: 233
package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgProjectEnvironmentEdgePolicyCloneAndDelete(t *testing.T) {
	store, ctx := pgStore(t)
	account, err := store.CreateAccount(ctx, "project-edge-policy@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "edge-policy", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "edge-policy-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.PutProjectEnvironmentEdgePolicy(ctx, state.ProjectEnvironmentEdgePolicy{
		AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "production",
		Rules: []state.ProjectEnvironmentEdgeRule{{
			Kind: state.EdgeRuleKindHeaders, MatchPath: "/", Priority: 100, Enabled: true,
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
				ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Environment", Value: "production", Action: "set"}},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, result, err := store.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{
		AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "staging",
	}, api.MustLimitsFor(account.Plan))
	if err != nil || result.PoliciesCopied != 1 {
		t.Fatalf("clone result=%+v err=%v", result, err)
	}
	cloned, err := store.GetProjectEnvironmentEdgePolicy(ctx, account.ID, app.ID, "staging")
	if err != nil || len(cloned.Rules) != 1 || cloned.Rules[0].Action.Headers.ResponseHeaders[0].Value != "production" {
		t.Fatalf("cloned policy=%+v err=%v", cloned, err)
	}
	if _, err := store.GetProjectEnvironmentEdgePolicy(ctx, "00000000-0000-0000-0000-000000000000", app.ID, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account lookup=%v", err)
	}
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetProjectEnvironmentEdgePolicy(ctx, account.ID, app.ID, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted policy=%v", err)
	}
}
