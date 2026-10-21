// adr: 233
package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemProjectEnvironmentEdgePolicyCloneAndDelete(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "environment-edge-policy@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "edge-shop", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "edge-shop-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetProjectEnvironmentEdgePolicy(ctx, account.ID, app.ID, "production"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing policy = %v, want ErrNotFound", err)
	}
	input := state.ProjectEnvironmentEdgePolicy{
		AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "production",
		Rules: []state.ProjectEnvironmentEdgeRule{{
			Kind: state.EdgeRuleKindHeaders, MatchPath: "/api/*", Priority: 100, Enabled: true,
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
				ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Environment", Value: "production", Action: "set"}},
			}},
		}},
	}
	if _, err := store.PutProjectEnvironmentEdgePolicy(ctx, input); err != nil {
		t.Fatal(err)
	}
	input.Rules[0].Action.Headers.ResponseHeaders[0].Value = "mutated"
	stored, err := store.GetProjectEnvironmentEdgePolicy(ctx, account.ID, app.ID, "production")
	if err != nil || stored.Rules[0].Action.Headers.ResponseHeaders[0].Value != "production" {
		t.Fatalf("policy alias leaked: %+v err=%v", stored, err)
	}
	_, result, err := store.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{
		AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "staging",
	}, api.MustLimitsFor(account.Plan))
	if err != nil || result.PoliciesCopied != 1 {
		t.Fatalf("clone result = %+v err=%v", result, err)
	}
	cloned, err := store.GetProjectEnvironmentEdgePolicy(ctx, account.ID, app.ID, "staging")
	if err != nil || cloned.Rules[0].Action.Headers.ResponseHeaders[0].Value != "production" {
		t.Fatalf("cloned policy = %+v err=%v", cloned, err)
	}
	cloned.Rules[0].Action.Headers.ResponseHeaders[0].Value = "staging"
	if _, err := store.PutProjectEnvironmentEdgePolicy(ctx, cloned); err != nil {
		t.Fatal(err)
	}
	stored, err = store.GetProjectEnvironmentEdgePolicy(ctx, account.ID, app.ID, "production")
	if err != nil || stored.Rules[0].Action.Headers.ResponseHeaders[0].Value != "production" {
		t.Fatalf("source changed after target update: %+v err=%v", stored, err)
	}
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetProjectEnvironmentEdgePolicy(ctx, account.ID, app.ID, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted policy = %v, want ErrNotFound", err)
	}
}

func TestEnvironmentEdgePolicyRejectsApplicationCORSPreset(t *testing.T) {
	presetID := "preset-id"
	rules := []state.ProjectEnvironmentEdgeRule{{
		Kind: state.EdgeRuleKindCORSA, MatchPath: "/", Enabled: true,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindCORSA, CORS: &state.EdgeRuleCORSAction{CorsPresetID: &presetID}},
	}}
	if state.ValidProjectEnvironmentEdgeRules(rules) {
		t.Fatal("environment-owned CORS must not reference an application-owned preset")
	}
}
