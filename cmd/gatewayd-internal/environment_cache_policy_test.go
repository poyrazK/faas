package main

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestEnvironmentCachePolicyGatewayIsolation(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "gateway-cache-policy@test.example", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "cache-policy"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "cache-policy", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	ctx = gateway.WithEdgeRuleApp(gateway.WithEdgeRuleOwner(ctx, account.ID), app.ID)
	staging, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	production, err := store.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: account.ID, AppID: app.ID, MatchHost: api.EdgeRuleAppHosts, MatchPath: "/**", Enabled: true, Priority: 1, Kind: state.EdgeRuleKindCache, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindCache, Cache: &state.EdgeRuleCacheAction{MaxAgeSeconds: 30}}})
	if err != nil {
		t.Fatal(err)
	}
	stageHost := gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, staging.ID, app.ID)
	productionHost := gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, production.ID, app.ID)
	matcher := newGatewaydEdgeRules(store, testLogger(), nil, nil)
	now := time.Now()
	matcher.cache.SetClock(func() time.Time { return now })
	matcher.clock = func() time.Time { return now }
	if got := matcher.MatchCache(ctx, stageHost, "/catalog/one", "GET"); got == nil || got.ID != parent.ID {
		t.Fatalf("initial inheritance=%+v", got)
	}
	initial, err := store.GetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	req := api.SetEnvironmentCachePolicyRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: staging.ID, RequestID: uuid.NewString(), ExpectedRevision: &initial.Revision, Rules: []api.EnvironmentCacheRule{{Path: "/catalog/*", Priority: 10, Cache: api.EdgeRuleCacheAction{MaxAgeSeconds: 120}}}}
	saved, err := store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", req)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * time.Second) // natural policy-cache expiry without a notification.
	stageRule := matcher.MatchCache(ctx, stageHost, "/catalog/one", "GET")
	if stageRule == nil || stageRule.ID == parent.ID || stageRule.MaxAgeSeconds != 120 {
		t.Fatalf("stage=%+v", stageRule)
	}
	if got := matcher.MatchCache(ctx, stageHost, "/other", "GET"); got != nil {
		t.Fatalf("parent cache leaked through override: %+v", got)
	}
	if got := matcher.MatchCache(ctx, productionHost, "/catalog/one", "GET"); got == nil || got.ID != parent.ID || got.MaxAgeSeconds != 30 {
		t.Fatalf("production=%+v", got)
	}
	req.RequestID = uuid.NewString()
	req.ExpectedRevision = &saved.Revision
	req.Rules[0].Cache.MaxAgeSeconds = 180
	revised, err := store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", req)
	if err != nil {
		t.Fatal(err)
	}
	matcher.Reset()
	if got := matcher.MatchCache(ctx, stageHost, "/catalog/one", "GET"); got == nil || got.ID == stageRule.ID || got.MaxAgeSeconds != 180 {
		t.Fatalf("revision did not partition cached bodies: %+v", got)
	}
	req.RequestID = uuid.NewString()
	req.ExpectedRevision = &revised.Revision
	req.Rules = []api.EnvironmentCacheRule{}
	if _, err = store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", req); err != nil {
		t.Fatal(err)
	}
	matcher.Reset()
	if got := matcher.MatchCache(ctx, stageHost, "/catalog/one", "GET"); got != nil {
		t.Fatalf("empty override still caches: %+v", got)
	}
	if got := matcher.MatchCache(ctx, productionHost, "/catalog/one", "GET"); got == nil || got.ID != parent.ID {
		t.Fatal("staging disabled production cache")
	}
}
