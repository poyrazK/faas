package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type cacheObserverTestStore struct {
	*state.MemStore
	fail   bool
	onRead func()
}

func (s *cacheObserverTestStore) GetEnvironmentCachePolicy(ctx context.Context, account, app, scope string) (api.EnvironmentCachePolicy, error) {
	if s.fail {
		return api.EnvironmentCachePolicy{}, errors.New("policy reader unavailable")
	}
	return s.MemStore.GetEnvironmentCachePolicy(ctx, account, app, scope)
}
func (s *cacheObserverTestStore) MatchEdgeRulesForHost(ctx context.Context, host string) ([]state.EdgeRule, error) {
	if s.onRead != nil {
		s.onRead()
	}
	return s.MemStore.MatchEdgeRulesForHost(ctx, host)
}

func TestEnvironmentCacheObservationRepairsWithoutNotifications(t *testing.T) {
	ctx := t.Context()
	s := &cacheObserverTestStore{MemStore: state.NewMemStore()}
	account, err := s.CreateAccount(ctx, "observer@test.example", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "observer"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "observer", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	role, target := "compute-node", "tcp://127.0.0.1:8080"
	node, err := s.CreateComputeNode(ctx, state.ComputeNode{Name: "cache-observer", TargetURL: "tcp://127.0.0.1:50051", GatewayTargetURL: &target, VPCPUs: 1, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 512, VCPUBudget: 1, Active: true, Role: &role})
	if err != nil {
		t.Fatal(err)
	}
	session := uuid.NewString()
	if err = s.BeginEnvironmentCacheGatewaySession(ctx, node.Name, session, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var revision int64
	req := api.SetEnvironmentCachePolicyRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, RequestID: uuid.NewString(), ExpectedRevision: &revision, Rules: []api.EnvironmentCacheRule{{Path: "/**", Priority: 1, Cache: api.EdgeRuleCacheAction{MaxAgeSeconds: 120}}}}
	saved, err := s.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", req)
	if err != nil {
		t.Fatal(err)
	}
	matcher := newGatewaydEdgeRules(s, testLogger(), nil, nil)
	if _, err = reconcileEnvironmentCachePage(ctx, s, matcher, node.Name, session, ""); err != nil {
		t.Fatal(err)
	}
	status, err := s.GetEnvironmentCacheConvergence(ctx, account.ID, app.ID, "staging")
	if err != nil || status.Status != "converged" {
		t.Fatalf("initial=%+v %v", status, err)
	}
	req.ExpectedRevision = &saved.Revision
	req.RequestID = uuid.NewString()
	req.Rules = []api.EnvironmentCacheRule{}
	newer, err := s.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", req)
	if err != nil {
		t.Fatal(err)
	}
	// Failed reads must never acknowledge last-known policy, including empty replacements.
	s.fail = true
	if _, err = reconcileEnvironmentCachePage(ctx, s, matcher, node.Name, session, ""); err == nil {
		t.Fatal("failed reader acknowledged policy")
	}
	status, _ = s.GetEnvironmentCacheConvergence(ctx, account.ID, app.ID, "staging")
	if status.Status != "pending" {
		t.Fatalf("failed read=%+v", status)
	}
	s.fail = false
	// A concurrent invalidation prevents installation; a parsed result is not an ack.
	s.onRead = func() { matcher.cache.Reset() }
	if _, err = reconcileEnvironmentCachePage(ctx, s, matcher, node.Name, session, ""); err == nil {
		t.Fatal("discarded cache generation acknowledged")
	}
	s.onRead = nil
	if _, err = reconcileEnvironmentCachePage(ctx, s, matcher, node.Name, session, ""); err != nil {
		t.Fatal(err)
	}
	status, _ = s.GetEnvironmentCacheConvergence(ctx, account.ID, app.ID, "staging")
	if status.Status != "converged" || status.Revision != newer.Revision {
		t.Fatalf("notification-free repair=%+v", status)
	}
	// Restoration is reconciled without notifications and loads inherited app rules.
	parent, err := s.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: account.ID, AppID: app.ID, MatchHost: api.EdgeRuleAppHosts, MatchPath: "/**", Enabled: true, Kind: state.EdgeRuleKindCache, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindCache, Cache: &state.EdgeRuleCacheAction{MaxAgeSeconds: 30}}})
	if err != nil {
		t.Fatal(err)
	}
	req.ExpectedRevision = &newer.Revision
	req.RequestID = uuid.NewString()
	req.Inherited = true
	restored, err := s.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", req)
	if err != nil {
		t.Fatal(err)
	}
	status, _ = s.GetEnvironmentCacheConvergence(ctx, account.ID, app.ID, "staging")
	if status.Status != "pending" {
		t.Fatal("old override ack counted for inheritance")
	}
	s.fail = true
	if _, err = reconcileEnvironmentCachePage(ctx, s, matcher, node.Name, session, ""); err == nil {
		t.Fatal("failed restoration load acknowledged")
	}
	s.fail = false
	if _, err = reconcileEnvironmentCachePage(ctx, s, matcher, node.Name, session, ""); err != nil {
		t.Fatal(err)
	}
	status, _ = s.GetEnvironmentCacheConvergence(ctx, account.ID, app.ID, "staging")
	if status.Status != "converged" || status.Revision != restored.Revision {
		t.Fatalf("restore convergence=%+v", status)
	}
	host := gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, env.ID, app.ID)
	entry, ok := matcher.cache.GetHost(gateway.EdgeRuleCacheKey(host, app.ID))
	if !ok || len(entry.Cache) != 1 || entry.Cache[0].ID == parent.ID {
		t.Fatal("restoration did not install revision-partitioned inherited cache rules")
	}
	restarted := uuid.NewString()
	if err = s.BeginEnvironmentCacheGatewaySession(ctx, node.Name, restarted, time.Now().UTC().Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	status, _ = s.GetEnvironmentCacheConvergence(ctx, account.ID, app.ID, "staging")
	if status.Status != "pending" {
		t.Fatal("restart reused old session")
	}
	matcher = newGatewaydEdgeRules(s, testLogger(), nil, nil)
	if _, err = reconcileEnvironmentCachePage(ctx, s, matcher, node.Name, restarted, ""); err != nil {
		t.Fatal(err)
	}
	status, _ = s.GetEnvironmentCacheConvergence(ctx, account.ID, app.ID, "staging")
	if status.Status != "converged" {
		t.Fatalf("restart recovery=%+v", status)
	}
}
