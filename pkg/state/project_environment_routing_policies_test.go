// adr: 233
package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemProjectEnvironmentRoutingPolicyCloneAndDelete(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "environment-routing-policy@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "routing-shop", ScanSource: ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "routing-api", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	input := ProjectEnvironmentEdgePolicy{
		AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "production",
		Rules: []ProjectEnvironmentEdgeRule{{
			Kind: EdgeRuleKindRedirect, MatchPath: "/old/*", Priority: 100, Enabled: true,
			Action: EdgeRuleAction{Kind: EdgeRuleKindRedirect, Redirect: &EdgeRuleRedirectAction{
				StatusCode: 308, To: "/new/$1", Headers: map[string]string{"X-Rule": "production"},
			}},
		}},
	}
	if _, err := store.PutProjectEnvironmentRoutingPolicy(ctx, input); err != nil {
		t.Fatal(err)
	}
	input.Rules[0].Action.Redirect.Headers["X-Rule"] = "mutated"
	stored, err := store.GetProjectEnvironmentRoutingPolicy(ctx, account.ID, app.ID, "production")
	if err != nil || stored.Rules[0].Action.Redirect.Headers["X-Rule"] != "production" {
		t.Fatalf("routing policy alias leaked: %+v err=%v", stored, err)
	}
	if _, err := store.GetProjectEnvironmentEdgePolicy(ctx, account.ID, app.ID, "production"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("routing policy created a headers/CORS override: %v", err)
	}
	_, result, err := store.CloneProjectEnvironment(ctx, ProjectEnvironmentClone{
		AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "staging",
	}, api.MustLimitsFor(account.Plan))
	if err != nil || result.PoliciesCopied != 1 {
		t.Fatalf("clone result=%+v err=%v", result, err)
	}
	cloned, err := store.GetProjectEnvironmentRoutingPolicy(ctx, account.ID, app.ID, "staging")
	if err != nil || len(cloned.Rules) != 1 || cloned.Rules[0].Action.Redirect.Headers["X-Rule"] != "production" {
		t.Fatalf("cloned routing policy=%+v err=%v", cloned, err)
	}
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetProjectEnvironmentRoutingPolicy(ctx, account.ID, app.ID, "staging"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted routing policy=%v", err)
	}
}

func TestEnvironmentRoutingPolicyRejectsOtherRuleKinds(t *testing.T) {
	if validProjectEnvironmentRoutingRules([]ProjectEnvironmentEdgeRule{{
		Kind: EdgeRuleKindHeaders, MatchPath: "/", Enabled: true,
		Action: EdgeRuleAction{Kind: EdgeRuleKindHeaders, Headers: &EdgeRuleHeadersAction{}},
	}}) {
		t.Fatal("routing policy accepted headers rule")
	}
}
