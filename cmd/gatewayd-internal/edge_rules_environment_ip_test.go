// adr: 233
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentIPPolicyIndependentReplacement(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "environment-ip-gateway@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "ip", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "ip-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	staging, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	host := gateway.BuildEnvironmentHost(".gregale.dev", staging.ID, app.ID)
	global, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
		AccountID: account.ID, AppID: app.ID, MatchHost: "*", MatchPath: "/", Priority: 100,
		Enabled: true, Kind: state.EdgeRuleKindIP,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Allow: []string{"198.51.100.0/24"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	matcher := newGatewaydEdgeRules(store, testLogger(), nil, nil)
	if got := matcher.MatchIP(ctx, host, "/", "GET"); got == nil || got.ID != global.ID {
		t.Fatalf("inherited IP rule = %+v", got)
	}
	_, err = store.PutProjectEnvironmentIPPolicy(ctx, state.ProjectEnvironmentEdgePolicy{
		AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "staging",
		Rules: []state.ProjectEnvironmentEdgeRule{{
			Kind: state.EdgeRuleKindIP, MatchPath: "/", Priority: 10, Enabled: true,
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Allow: []string{"192.0.2.0/24"}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	matcher.Reset()
	if got := matcher.MatchIP(ctx, host, "/", "GET"); got == nil || got.ID == global.ID || len(got.Allow) != 1 || got.Allow[0].String() != "192.0.2.0/24" {
		t.Fatalf("scoped IP rule = %+v", got)
	}
	if got := matcher.MatchIP(ctx, "ip.example.com", "/", "GET"); got == nil || got.ID != global.ID {
		t.Fatalf("ordinary host IP rule changed: %+v", got)
	}
	_, err = store.PutProjectEnvironmentIPPolicy(ctx, state.ProjectEnvironmentEdgePolicy{
		AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "staging", Rules: []state.ProjectEnvironmentEdgeRule{},
	})
	if err != nil {
		t.Fatal(err)
	}
	matcher.Reset()
	if got := matcher.MatchIP(ctx, host, "/", "GET"); got != nil {
		t.Fatalf("empty scoped policy inherited global IP rule: %+v", got)
	}
	failed := newGatewaydEdgeRules(failingEnvironmentIPStore{MemStore: store}, testLogger(), nil, nil)
	if err := failed.EnsureEnvironmentPolicy(ctx, host); err == nil {
		t.Fatal("environment IP policy read error did not fail closed")
	}
}

type failingEnvironmentIPStore struct{ *state.MemStore }

func (s failingEnvironmentIPStore) GetProjectEnvironmentIPPolicy(context.Context, string, string, string) (state.ProjectEnvironmentEdgePolicy, error) {
	return state.ProjectEnvironmentEdgePolicy{}, errors.New("IP policy read unavailable")
}

func TestEnvironmentIPPolicyGuardFailsClosedOnStoreError(t *testing.T) {
	store := &fakeEdgeRuleStore{err: errors.New("store unavailable")}
	matcher := newGatewaydEdgeRules(store, testLogger(), nil, nil)
	host := gateway.BuildEnvironmentHost(".gregale.dev", "ce1639c6-eec7-4115-a98a-661d910bd3e1", "60f9c408-105e-4617-af50-b4d48bb5d910")
	if err := matcher.EnsureEnvironmentPolicy(context.Background(), host); err == nil {
		t.Fatal("environment policy guard allowed request during store failure")
	}
}
