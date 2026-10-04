// adr: 531
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeclaredRouteSnapshotSurvivesInvalidationAndDocumentFailure(t *testing.T) {
	store := &declaredRouteDocStoreStub{doc: []byte(`{"openapi":"3.0.0","paths":{"/old/{id}":{"get":{}}}}`)}
	matcher := newDeclaredRoutesMatcher(store)
	app := gateway.App{ID: "app", AccountID: "owner", OnlyAllowDeclaredRoutes: true}
	ctx, resolved, before, err := matcher.PinDeclaredRoutePolicy(t.Context(), app)
	if err != nil || before == "" {
		t.Fatalf("pin=%s/%v", before, err)
	}
	store.doc = []byte(`{"openapi":"3.0.0","paths":{"/new":{"post":{}}}}`)
	matcher.Invalidate(app.ID)
	fresh, _, after, err := matcher.PinDeclaredRoutePolicy(t.Context(), app)
	if err != nil || before == after {
		t.Fatalf("changed contract kept digest: %s/%s/%v", before, after, err)
	}
	if allowed, err := matcher.MatchDeclaredRoute(ctx, resolved, "/old/123", "GET"); err != nil || !allowed {
		t.Fatalf("old request lost its declaration: %v/%v", allowed, err)
	}
	if template, matched, err := matcher.ResolveObservedRoute(ctx, resolved, "/old/123", "GET"); err != nil || !matched || template != "/old/{id}" {
		t.Fatalf("route evidence drifted: %q/%v/%v", template, matched, err)
	}
	if allowed, err := matcher.MatchDeclaredRoute(ctx, resolved, "/new", "POST"); err != nil || allowed {
		t.Fatalf("new declaration entered old request: %v/%v", allowed, err)
	}
	if allowed, err := matcher.MatchDeclaredRoute(fresh, resolved, "/new", "POST"); err != nil || !allowed {
		t.Fatalf("fresh request missed changed contract: %v/%v", allowed, err)
	}
	matcher.Invalidate(app.ID)
	store.err = errors.New("document store unavailable")
	if allowed, err := matcher.MatchDeclaredRoute(ctx, resolved, "/old/123", "HEAD"); err != nil || !allowed {
		t.Fatalf("pinned contract failed during outage: %v/%v", allowed, err)
	}
	if _, _, _, err := matcher.PinDeclaredRoutePolicy(t.Context(), app); err == nil {
		t.Fatal("unverified new document policy was accepted")
	}
	foreign := app
	foreign.AccountID = "different"
	if _, err := matcher.MatchDeclaredRoute(ctx, foreign, "/old/123", "GET"); err == nil {
		t.Fatal("pinned policy crossed owner identity")
	}
}

func TestDeclaredRouteSnapshotDigestIsCanonicalAndExplicitSourcesAreCopied(t *testing.T) {
	one, err := compileDeclaredRoutes([]gateway.DeclaredRoute{{Path: "/b", Methods: []string{"POST", "GET"}}, {Path: "/a", Methods: []string{"HEAD"}}})
	if err != nil {
		t.Fatal(err)
	}
	two, err := compileDeclaredRoutes([]gateway.DeclaredRoute{{Path: "/a", Methods: []string{"HEAD"}}, {Path: "/b", Methods: []string{"GET", "POST"}}})
	if err != nil || one.revision != two.revision {
		t.Fatalf("equivalent effective route sets changed digest: %s/%s/%v", one.revision, two.revision, err)
	}
	matcher := newDeclaredRoutesMatcher(nil)
	app := gateway.App{ID: "app", AccountID: "owner", DeclaredRoutes: []gateway.DeclaredRoute{{Path: "/a", Methods: []string{"GET"}}}}
	ctx, resolved, _, err := matcher.PinDeclaredRoutePolicy(t.Context(), app)
	if err != nil {
		t.Fatal(err)
	}
	app.DeclaredRoutes[0].Methods[0] = "POST"
	if resolved.DeclaredRoutes[0].Methods[0] != "GET" {
		t.Fatal("pinned app retained mutable route methods")
	}
	if allowed, err := matcher.MatchDeclaredRoute(ctx, resolved, "/a", "GET"); err != nil || !allowed {
		t.Fatalf("source mutation changed compiled match: %v/%v", allowed, err)
	}
}

func TestDeclaredRouteSnapshotRetainsScopedOverlay(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "route-snapshot@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "snapshot", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "snapshot-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	policy := state.ProjectEnvironmentRoutePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "staging", OnlyAllowDeclaredRoutes: true, DeclaredRoutes: []state.DeclaredRoute{{Path: "/old", Methods: []string{"GET"}}}}
	if _, err := store.PutProjectEnvironmentRoutePolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	matcher := newDeclaredRoutesMatcher(store)
	scoped := gateway.App{ID: app.ID, AccountID: account.ID, PinnedDeploymentScope: "staging"}
	pinned, resolved, before, err := matcher.PinDeclaredRoutePolicy(ctx, scoped)
	if err != nil || !resolved.OnlyAllowDeclaredRoutes {
		t.Fatalf("scoped pin=%v/%v", resolved, err)
	}
	policy.OnlyAllowDeclaredRoutes = false
	policy.DeclaredRoutes = []state.DeclaredRoute{{Path: "/new", Methods: []string{"POST"}}}
	if _, err := store.PutProjectEnvironmentRoutePolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	retained, err := matcher.ResolveScopedRoutePolicy(pinned, scoped)
	if err != nil || !retained.OnlyAllowDeclaredRoutes || retained.DeclaredRoutes[0].Path != "/old" {
		t.Fatalf("scoped update changed admitted inputs: %v/%v", retained, err)
	}
	_, fresh, after, err := matcher.PinDeclaredRoutePolicy(ctx, scoped)
	if err != nil || fresh.OnlyAllowDeclaredRoutes || before == after {
		t.Fatalf("fresh scoped update missing: %v/%s/%s/%v", fresh, before, after, err)
	}
}

type ownerScopedRouteStore struct {
	declaredRouteDocStoreStub
	owner string
}

func (s *ownerScopedRouteStore) GetAppOpenAPIDoc(ctx context.Context, app, account string) ([]byte, state.AppOpenAPIDocMeta, error) {
	if account != s.owner {
		return nil, state.AppOpenAPIDocMeta{}, state.ErrNotFound
	}
	return s.declaredRouteDocStoreStub.GetAppOpenAPIDoc(ctx, app, account)
}

func TestDeclaredRouteCacheDoesNotReuseAnotherOwnersContract(t *testing.T) {
	store := &ownerScopedRouteStore{owner: "owner", declaredRouteDocStoreStub: declaredRouteDocStoreStub{doc: []byte(`{"openapi":"3.0.0","paths":{"/private":{"get":{}}}}`)}}
	matcher := newDeclaredRoutesMatcher(store)
	app := gateway.App{ID: "app", AccountID: "owner", OnlyAllowDeclaredRoutes: true}
	ctx, _, _, err := matcher.PinDeclaredRoutePolicy(t.Context(), app)
	if err != nil {
		t.Fatal(err)
	}
	foreign := app
	foreign.AccountID = "foreign"
	if allowed, err := matcher.MatchDeclaredRoute(ctx, foreign, "/private", "GET"); allowed || !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cached owner contract crossed identity: %v/%v", allowed, err)
	}
}

type blockedRouteDocStore struct{ started, release chan struct{} }

func (s *blockedRouteDocStore) GetAppOpenAPIDoc(ctx context.Context, _ string, _ string) ([]byte, state.AppOpenAPIDocMeta, error) {
	close(s.started)
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, state.AppOpenAPIDocMeta{}, ctx.Err()
	}
	return []byte(`{"openapi":"3.0.0","paths":{"/old":{"get":{}}}}`), state.AppOpenAPIDocMeta{}, nil
}

func TestDeclaredRouteInvalidationFencesAnUnpublishedSnapshotLoad(t *testing.T) {
	store := &blockedRouteDocStore{started: make(chan struct{}), release: make(chan struct{})}
	matcher := newDeclaredRoutesMatcher(store)
	result := make(chan error, 1)
	go func() {
		_, _, _, err := matcher.PinDeclaredRoutePolicy(t.Context(), gateway.App{ID: "app", AccountID: "owner"})
		result <- err
	}()
	<-store.started
	matcher.Invalidate("different-app")
	matcher.Invalidate("app")
	close(store.release)
	if err := <-result; err == nil {
		t.Fatal("old load published after its app contract changed")
	}
	matcher.mu.RLock()
	defer matcher.mu.RUnlock()
	if len(matcher.entries) != 0 || len(matcher.pending) != 0 {
		t.Fatal("invalidated load remained cached or tracked")
	}
}
