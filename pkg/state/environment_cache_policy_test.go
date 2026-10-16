package state_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentCachePolicyMemory(t *testing.T) {
	testEnvironmentCachePolicy(t, state.NewMemStore())
}
func TestEnvironmentCachePolicyPostgres(t *testing.T) {
	testEnvironmentCachePolicy(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testEnvironmentCachePolicy(t *testing.T, s state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, uuid.NewString()+"@cache-policy.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "cache-policy"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "cache-policy", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	store := s.(state.EnvironmentCachePolicyStore)
	initial, err := store.GetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging")
	if err != nil || !initial.Inherited || initial.Revision != 0 || initial.EnvironmentID != env.ID {
		t.Fatalf("initial=%+v %v", initial, err)
	}
	parent, err := s.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: account.ID, AppID: app.ID, MatchHost: api.EdgeRuleAppHosts, MatchPath: "/**", Enabled: true, Kind: state.EdgeRuleKindCache, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindCache, Cache: &state.EdgeRuleCacheAction{MaxAgeSeconds: 30}}})
	if err != nil {
		t.Fatal(err)
	}
	req := api.SetEnvironmentCachePolicyRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, RequestID: uuid.NewString(), ExpectedRevision: &initial.Revision, Rules: []api.EnvironmentCacheRule{{Path: "/catalog/*", Priority: 10, Cache: api.EdgeRuleCacheAction{MaxAgeSeconds: 120}}}}
	saved, err := store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", req)
	if err != nil || saved.Inherited || saved.Revision != 1 {
		t.Fatalf("saved=%+v %v", saved, err)
	}
	saved.Rules[0].Cache.MaxAgeSeconds = 999 // callers cannot mutate saved intent.
	current, err := store.GetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging")
	if err != nil || current.Rules[0].Cache.MaxAgeSeconds != 120 {
		t.Fatalf("alias: %+v %v", current, err)
	}
	neighbor, err := store.GetEnvironmentCachePolicy(ctx, account.ID, app.ID, "production")
	if err != nil || !neighbor.Inherited || neighbor.Revision != 0 {
		t.Fatal("staging changed production")
	}
	rules, err := s.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil || len(rules) != 1 || rules[0].ID != parent.ID || rules[0].Action.Cache.MaxAgeSeconds != 30 {
		t.Fatal("environment changed shared app policy")
	}
	// The stable receipt survives later writes and carries the original result.
	empty := req
	empty.RequestID = uuid.NewString()
	empty.ExpectedRevision = &current.Revision
	empty.Rules = []api.EnvironmentCacheRule{}
	disabled, err := store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", empty)
	if err != nil || disabled.Inherited || len(disabled.Rules) != 0 {
		t.Fatal("empty override did not disable caching", err)
	}
	replay, err := store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", req)
	if err != nil || replay.Revision != 1 || replay.Rules[0].Cache.MaxAgeSeconds != 120 {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	altered := req
	altered.Rules = []api.EnvironmentCacheRule{}
	if _, err = store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", altered); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("request reuse=%v", err)
	}
	stale := req
	stale.RequestID = uuid.NewString()
	if _, err = store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", stale); !errors.Is(err, state.ErrRouteHealthRevision) {
		t.Fatalf("stale=%v", err)
	}
	// Restoration keeps a positive revision and cannot resurrect older reviews.
	restore := empty
	restore.RequestID = uuid.NewString()
	restoreRevision := disabled.Revision
	restore.ExpectedRevision = &restoreRevision
	restore.Inherited = true
	restored, err := store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", restore)
	if err != nil || !restored.Inherited || restored.Revision != disabled.Revision+1 || len(restored.Rules) != 0 || restored.UpdatedAt == nil {
		t.Fatalf("restored=%+v %v", restored, err)
	}
	applied, err := state.ApplyEnvironmentCachePolicy(rules, restored, "environment.test")
	if err != nil || len(applied) != 1 || applied[0].Action.Cache.MaxAgeSeconds != 30 || applied[0].ID == parent.ID || rules[0].ID != parent.ID {
		t.Fatalf("inherited rules=%+v %v", applied, err)
	}
	if _, err = store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", stale); !errors.Is(err, state.ErrRouteHealthRevision) {
		t.Fatalf("restoration accepted old review: %v", err)
	}
	altered = restore
	altered.Inherited = false // [] disables caching, distinct from inheritance.
	if _, err = store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", altered); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("restore ID reused for disable: %v", err)
	}
	bad := restore
	bad.RequestID = uuid.NewString()
	bad.ExpectedRevision = &restored.Revision
	bad.Rules = req.Rules
	if _, err = store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", bad); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("inherited override accepted: %v", err)
	}
	disabled = restored
	// Concurrent override/restoration reviews at one revision have exactly one winner.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := range 2 {
		change := req
		if i == 1 {
			change.Inherited = true
			change.Rules = []api.EnvironmentCacheRule{}
		}
		change.RequestID = uuid.NewString()
		change.ExpectedRevision = &disabled.Revision
		wg.Go(func() {
			_, err := store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", change)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, state.ErrRouteHealthRevision) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
	replay, err = store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", restore)
	if err != nil || replay.Revision != restored.Revision || !replay.Inherited {
		t.Fatalf("restore receipt after later write=%+v %v", replay, err)
	}
	if _, err = store.GetEnvironmentCachePolicy(ctx, uuid.NewString(), app.ID, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign owner=%v", err)
	}
	// Reusing a slug cannot recover old rules or old successful receipts.
	if err = s.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := store.GetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging")
	if err != nil || !fresh.Inherited || fresh.EnvironmentID != replacement.ID || fresh.EnvironmentID == env.ID {
		t.Fatalf("recreated=%+v %v", fresh, err)
	}
	if _, err = store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", req); !errors.Is(err, state.ErrRouteHealthRevision) {
		t.Fatalf("old identity replay=%v", err)
	}
	if reflect.DeepEqual(fresh.Rules, req.Rules) {
		t.Fatal("replacement inherited deleted environment rules")
	}
}
