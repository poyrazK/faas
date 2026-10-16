package state_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentCacheCopyMemory(t *testing.T) { testEnvironmentCacheCopy(t, state.NewMemStore()) }
func TestEnvironmentCacheCopyPostgres(t *testing.T) {
	testEnvironmentCacheCopy(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testEnvironmentCacheCopy(t *testing.T, s state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, uuid.NewString()+"@cache-copy.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "cache-copy"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "cache-copy", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	store := s.(state.EnvironmentCachePolicyStore)
	read := func(scope string) api.EnvironmentCachePolicy {
		t.Helper()
		p, e := store.GetEnvironmentCachePolicy(ctx, account.ID, app.ID, scope)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	request := func(p api.EnvironmentCachePolicy, rules []api.EnvironmentCacheRule) api.SetEnvironmentCachePolicyRequest {
		return api.SetEnvironmentCachePolicyRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: p.EnvironmentID, RequestID: uuid.NewString(), ExpectedRevision: &p.Revision, Rules: rules}
	}
	write := func(scope string, req api.SetEnvironmentCachePolicyRequest) api.EnvironmentCachePolicy {
		t.Helper()
		p, e := store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, scope, req)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	rules := []api.EnvironmentCacheRule{{Path: "/catalog/*", Priority: 1, Cache: api.EdgeRuleCacheAction{MaxAgeSeconds: 120}}}
	source := write("staging", request(read("staging"), rules))
	dest := read("production")
	copyReq := request(dest, source.Rules)
	copyReq.CopyFrom = &api.EnvironmentCachePolicySource{Scope: "staging", EnvironmentID: source.EnvironmentID, Revision: source.Revision}
	bad := copyReq
	bad.Rules = []api.EnvironmentCacheRule{}
	if _, err = store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "production", bad); !errors.Is(err, state.ErrRouteHealthRevision) {
		t.Fatalf("unreviewed source contents: %v", err)
	}
	copied := write("production", copyReq)
	if copied.Revision != 1 || copied.Inherited || !reflect.DeepEqual(copied.Rules, source.Rules) {
		t.Fatalf("copied=%+v", copied)
	}
	sourceRules, err := state.ApplyEnvironmentCachePolicy(nil, source, "source.test")
	if err != nil {
		t.Fatal(err)
	}
	destRules, err := state.ApplyEnvironmentCachePolicy(nil, copied, "destination.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceRules) != 1 || len(destRules) != 1 || sourceRules[0].ID == destRules[0].ID {
		t.Fatal("copied environments share response-cache identities")
	}
	if after := read("staging"); !reflect.DeepEqual(after, source) {
		t.Fatal("copy mutated source")
	}
	// Both policies may change after commit; the original receipt still recovers.
	disabled := write("staging", request(source, []api.EnvironmentCacheRule{}))
	changed := write("production", request(copied, []api.EnvironmentCacheRule{}))
	replay := write("production", copyReq)
	replayJSON, _ := json.Marshal(replay)
	copiedJSON, _ := json.Marshal(copied)
	if string(replayJSON) != string(copiedJSON) {
		t.Fatalf("receipt recovery=%s want %s", replayJSON, copiedJSON)
	}
	if after := read("production"); !reflect.DeepEqual(after, changed) {
		t.Fatal("receipt overwrote later destination")
	}
	stale := copyReq
	stale.RequestID = uuid.NewString()
	stale.ExpectedRevision = &changed.Revision
	if _, err = store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "production", stale); !errors.Is(err, state.ErrRouteHealthRevision) {
		t.Fatalf("source revision guard: %v", err)
	}
	reused := copyReq
	pin := *copyReq.CopyFrom
	pin.Revision = disabled.Revision
	reused.CopyFrom = &pin
	if _, err = store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "production", reused); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("copy pin changed under retry ID: %v", err)
	}
	// An explicit empty source copies cache-disabled mode, never inheritance.
	empty := request(changed, disabled.Rules)
	empty.CopyFrom = &api.EnvironmentCachePolicySource{Scope: "staging", EnvironmentID: disabled.EnvironmentID, Revision: disabled.Revision}
	emptyCopy := write("production", empty)
	if emptyCopy.Inherited || len(emptyCopy.Rules) != 0 {
		t.Fatal("empty copy restored inheritance")
	}
	staleDest := request(changed, disabled.Rules)
	staleDest.CopyFrom = empty.CopyFrom
	if _, err = store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "production", staleDest); !errors.Is(err, state.ErrRouteHealthRevision) {
		t.Fatalf("destination revision guard: %v", err)
	}
	restore := request(disabled, []api.EnvironmentCacheRule{})
	restore.Inherited = true
	inherited := write("staging", restore)
	noOverride := request(emptyCopy, []api.EnvironmentCacheRule{})
	noOverride.CopyFrom = &api.EnvironmentCachePolicySource{Scope: "staging", EnvironmentID: inherited.EnvironmentID, Revision: inherited.Revision}
	if _, err = store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "production", noOverride); !errors.Is(err, state.ErrRouteHealthRevision) {
		t.Fatalf("inherited source copied: %v", err)
	}
	// Opposite copies lock the same app first; only the first reviewed copy wins.
	a := write("staging", request(inherited, rules))
	b := read("production")
	ab := request(b, a.Rules)
	ab.CopyFrom = &api.EnvironmentCachePolicySource{Scope: a.Scope, EnvironmentID: a.EnvironmentID, Revision: a.Revision}
	ba := request(a, b.Rules)
	ba.CopyFrom = &api.EnvironmentCachePolicySource{Scope: b.Scope, EnvironmentID: b.EnvironmentID, Revision: b.Revision}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, op := range []struct {
		scope string
		req   api.SetEnvironmentCachePolicyRequest
	}{{"production", ab}, {"staging", ba}} {
		wg.Go(func() { _, e := store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, op.scope, op.req); errs <- e })
	}
	wg.Wait()
	close(errs)
	wins, conflicts := 0, 0
	for e := range errs {
		if e == nil {
			wins++
		} else if errors.Is(e, state.ErrRouteHealthRevision) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("opposite copies: wins=%d conflicts=%d", wins, conflicts)
	}
	// Recreating a source slug cannot recover a receipt bound to its old UUID.
	if err = s.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "production", copyReq); !errors.Is(err, state.ErrRouteHealthRevision) {
		t.Fatalf("recreated source reused receipt: %v", err)
	}
}
