// adr: 233
package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemProjectEnvironmentIPPolicyCloneAndDelete(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "environment-ip-policy@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "ip-shop", ScanSource: ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "ip-api", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	input := ProjectEnvironmentEdgePolicy{
		AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "production",
		Rules: []ProjectEnvironmentEdgeRule{{
			Kind: EdgeRuleKindIP, MatchPath: "/", Priority: 100, Enabled: true,
			Action: EdgeRuleAction{Kind: EdgeRuleKindIP, IP: &EdgeRuleIPAction{Allow: []string{"192.0.2.0/24"}}},
		}},
	}
	if _, err := store.PutProjectEnvironmentIPPolicy(ctx, input); err != nil {
		t.Fatal(err)
	}
	input.Rules[0].Action.IP.Allow[0] = "0.0.0.0/0"
	stored, err := store.GetProjectEnvironmentIPPolicy(ctx, account.ID, app.ID, "production")
	if err != nil || stored.Rules[0].Action.IP.Allow[0] != "192.0.2.0/24" {
		t.Fatalf("IP policy alias leaked: %+v err=%v", stored, err)
	}
	if _, err := store.GetProjectEnvironmentRoutingPolicy(ctx, account.ID, app.ID, "production"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("IP policy created routing override: %v", err)
	}
	_, result, err := store.CloneProjectEnvironment(ctx, ProjectEnvironmentClone{
		AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "staging",
	}, api.MustLimitsFor(account.Plan))
	if err != nil || result.PoliciesCopied != 1 {
		t.Fatalf("clone result=%+v err=%v", result, err)
	}
	cloned, err := store.GetProjectEnvironmentIPPolicy(ctx, account.ID, app.ID, "staging")
	if err != nil || len(cloned.Rules) != 1 || cloned.Rules[0].Action.IP.Allow[0] != "192.0.2.0/24" {
		t.Fatalf("cloned IP policy=%+v err=%v", cloned, err)
	}
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetProjectEnvironmentIPPolicy(ctx, account.ID, app.ID, "staging"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted IP policy=%v", err)
	}
}

func TestEnvironmentIPPolicyRejectsMalformedRules(t *testing.T) {
	for _, rule := range []ProjectEnvironmentEdgeRule{
		{Kind: EdgeRuleKindHeaders, MatchPath: "/", Action: EdgeRuleAction{Kind: EdgeRuleKindHeaders, Headers: &EdgeRuleHeadersAction{}}},
		{Kind: EdgeRuleKindIP, MatchPath: "/[", Action: EdgeRuleAction{Kind: EdgeRuleKindIP, IP: &EdgeRuleIPAction{Allow: []string{"192.0.2.0/24"}}}},
		{Kind: EdgeRuleKindIP, MatchPath: "/", Action: EdgeRuleAction{Kind: EdgeRuleKindIP, IP: &EdgeRuleIPAction{Allow: []string{"not-a-cidr"}}}},
	} {
		if ValidProjectEnvironmentIPRules([]ProjectEnvironmentEdgeRule{rule}) {
			t.Fatalf("accepted invalid IP rule: %+v", rule)
		}
	}
}
