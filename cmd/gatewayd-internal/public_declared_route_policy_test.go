// adr: 375
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func (f publicRoutingPGFixture) importedContract(t *testing.T, route string) {
	t.Helper()
	doc, err := json.Marshal(map[string]any{"openapi": "3.0.0", "paths": map[string]any{route: map[string]any{"get": map[string]any{}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpsertAppOpenAPIDoc(t.Context(), f.app.ID, f.app.AccountID, doc, 1, "3.0.0"); err != nil {
		t.Fatal(err)
	}
}

func assertContractMatch(t *testing.T, matcher *declaredRoutesMatcher, ctx context.Context, app gateway.App, route string, want bool) {
	t.Helper()
	allowed, err := matcher.MatchDeclaredRoute(ctx, app, route, "GET")
	if err != nil || allowed != want {
		t.Fatalf("contract match %s = %v/%v; want %v", route, allowed, err, want)
	}
}

func TestPublicDeclaredContractPostgresAppAndDocumentShareOneView(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:contract-view")
	f.importedContract(t, "/old/{id}")
	if _, err := f.pool.Exec(t.Context(), `UPDATE apps SET only_declared_routes=true WHERE id=$1`, f.app.ID); err != nil {
		t.Fatal(err)
	}
	before := f.routingApp()
	router := pgRouter{appsSuffix: ".apps.gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
	var changed atomic.Bool
	router.store = publicHostAfterAppStore{PgStore: f.store, after: func() {
		if changed.Swap(true) {
			return
		}
		f.importedContract(t, "/new")
		if _, err := f.pool.Exec(t.Context(), `UPDATE apps SET only_declared_routes=false WHERE id=$1`, f.app.ID); err != nil {
			t.Fatal(err)
		}
	}}
	during, found, err := router.ResolveHost(t.Context(), f.app.Slug+router.appsSuffix)
	if err != nil || !found || !during.OnlyAllowDeclaredRoutes || during.PublicPolicySource.Revision != before.PublicPolicySource.Revision {
		t.Fatalf("document/app generations mixed: found=%v err=%v", found, err)
	}
	// The production carrier must not load a second store generation, even
	// when the separate document cache/store is unavailable.
	store := &declaredRouteDocStoreStub{err: errors.New("separate store unavailable")}
	matcher := newDeclaredRoutesMatcher(store)
	oldContext, oldApp, _, err := matcher.PinDeclaredRoutePolicy(t.Context(), during)
	if err != nil || store.reads != 0 {
		t.Fatalf("snapshot caused a second document read: %v reads=%d", err, store.reads)
	}
	assertContractMatch(t, matcher, oldContext, oldApp, "/old/123", true)
	assertContractMatch(t, matcher, oldContext, oldApp, "/new", false)
	inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "production"}
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), during, inputs); err == nil {
		t.Fatal("old contract combined with new dispatch policy")
	}
	fresh := f.routingApp()
	newContext, newApp, _, err := matcher.PinDeclaredRoutePolicy(t.Context(), fresh)
	if err != nil || fresh.OnlyAllowDeclaredRoutes || fresh.PublicPolicySource.Revision == before.PublicPolicySource.Revision {
		t.Fatalf("fresh document/app generation missing: %v", err)
	}
	assertContractMatch(t, matcher, newContext, newApp, "/old/123", false)
	assertContractMatch(t, matcher, newContext, newApp, "/new", true)
	assertContractMatch(t, matcher, oldContext, oldApp, "/old/123", true)
	if template, matched, err := matcher.ResolveObservedRoute(oldContext, oldApp, "/old/123", "GET"); err != nil || !matched || template != "/old/{id}" {
		t.Fatalf("observation changed its admitted contract: %s/%v/%v", template, matched, err)
	}
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), fresh, inputs); err != nil {
		t.Fatalf("fresh contract did not recover: %v", err)
	}
}

func TestPublicDeclaredContractPostgresMissingCreatedAndDeletedWithoutNotify(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:contract-missing")
	missing := f.routingApp()
	matcher := newDeclaredRoutesMatcher(f.store)
	missingContext, missingApp, revision, err := matcher.PinDeclaredRoutePolicy(t.Context(), missing)
	if err != nil || revision == "" || missing.ImportedRoutePolicy == nil || missing.ImportedRoutePolicy.Found {
		t.Fatalf("missing contract lacks authoritative evidence: %s/%v", revision, err)
	}
	inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "production"}
	f.importedContract(t, "/created")
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), missing, inputs); err == nil {
		t.Fatal("old missing-document baseline survived a new import")
	}
	created := f.routingApp()
	createdContext, createdApp, _, err := matcher.PinDeclaredRoutePolicy(t.Context(), created)
	if err != nil {
		t.Fatal(err)
	}
	assertContractMatch(t, matcher, createdContext, createdApp, "/created", true)
	if _, err := matcher.MatchDeclaredRoute(missingContext, missingApp, "/created", "GET"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("new import entered old missing contract: %v", err)
	}
	if err := f.store.DeleteAppOpenAPIDoc(t.Context(), f.app.ID, f.app.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), created, inputs); err == nil {
		t.Fatal("deleted document survived dispatch verification")
	}
	deleted := f.routingApp()
	if _, err := matcher.MatchDeclaredRoute(t.Context(), deleted, "/created", "GET"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("compilation cache hid document deletion: %v", err)
	}
	assertContractMatch(t, matcher, createdContext, createdApp, "/created", true)
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), deleted, inputs); err != nil {
		t.Fatalf("fresh missing contract refused: %v", err)
	}
}

func TestPublicDeclaredContractPostgresScopedOverlayAndFallback(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:contract-prod")
	stage := f.deployment(t, "staging", "sha256:contract-stage")
	if _, err := f.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: f.app.AccountID, ProjectID: f.project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	f.importedContract(t, "/imported")
	if _, err := f.pool.Exec(t.Context(), `UPDATE apps SET only_declared_routes=true WHERE id=$1`, f.app.ID); err != nil {
		t.Fatal(err)
	}
	policy := state.ProjectEnvironmentRoutePolicy{AccountID: f.app.AccountID, ProjectID: f.project.ID, AppID: f.app.ID, EnvironmentSlug: "staging", OnlyAllowDeclaredRoutes: true,
		DeclaredRoutes: []state.DeclaredRoute{{Path: "/stage", Methods: []string{"GET"}}}}
	if _, err := f.store.PutProjectEnvironmentRoutePolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", deploySuffix: ".gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
	host := gateway.BuildDeploymentPreviewURL(router.deploySuffix, stage.Revision, f.app.Slug)
	app, found, err := router.ResolveHost(t.Context(), host)
	if err != nil || !found || !app.OnlyAllowDeclaredRoutes || len(app.DeclaredRoutes) != 1 || app.DeclaredRoutes[0].Path != "/stage" || app.ImportedRoutePolicy.Found {
		t.Fatalf("scoped contract missing: found=%v err=%v app=%+v", found, err, app.DeclaredRoutes)
	}
	matcher := newDeclaredRoutesMatcher(f.store)
	ctx, admitted, _, err := matcher.PinDeclaredRoutePolicy(t.Context(), app)
	if err != nil {
		t.Fatal(err)
	}
	assertContractMatch(t, matcher, ctx, admitted, "/stage", true)
	assertContractMatch(t, matcher, ctx, admitted, "/imported", false)
	assertContractMatch(t, matcher, t.Context(), f.routingApp(), "/imported", true)
	inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "staging", HostDeploymentID: stage.ID, HostScope: "staging"}
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), app, inputs); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `DELETE FROM project_environment_route_policies WHERE app_id=$1 AND environment_slug='staging'`, f.app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), app, inputs); err == nil {
		t.Fatal("scoped deletion combined old contract and new dispatch")
	}
	fresh, found, err := router.ResolveHost(t.Context(), host)
	if err != nil || !found || len(fresh.DeclaredRoutes) != 0 || !fresh.ImportedRoutePolicy.Found {
		t.Fatalf("deleted overlay did not use application fallback: %v/%v", found, err)
	}
	assertContractMatch(t, matcher, t.Context(), fresh, "/imported", true)
	assertContractMatch(t, matcher, ctx, admitted, "/stage", true)
	if _, err := f.store.PutProjectEnvironmentRoutePolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE project_environment_route_policies
		SET declared_routes=jsonb_build_array(jsonb_build_object('path','/'||repeat('x',$2::integer),'methods',jsonb_build_array('GET')))
		WHERE app_id=$1 AND environment_slug='staging'`, f.app.ID, api.TrafficPolicyMaxContractBytes-128); err != nil {
		t.Fatal(err)
	}
	if err := f.store.WithPublicHostPolicySnapshot(t.Context(), func(reader state.PublicHostPolicyReader) error {
		oversized, err := reader.PublicHostRoutePolicy(t.Context(), f.app.AccountID, f.app.ID, "staging")
		if err == nil || len(oversized.DeclaredRoutes) != 0 {
			t.Fatalf("oversized scoped contract transferred: routes=%d err=%v", len(oversized.DeclaredRoutes), err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := router.ResolveHost(t.Context(), host); err == nil || found {
		t.Fatalf("oversized scoped contract resolved: found=%v err=%v", found, err)
	}
}

func TestPublicDeclaredContractPostgresExplicitRoutesSkipUnavailableImport(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:explicit-contract")
	f.importedContract(t, "/unused")
	if _, err := f.pool.Exec(t.Context(), `UPDATE apps SET only_declared_routes=true, declared_routes='[{"path":"/explicit","methods":["GET"]}]' WHERE id=$1`, f.app.ID); err != nil {
		t.Fatal(err)
	}
	lock, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(t.Context(), `LOCK TABLE app_openapi_docs IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	app := f.routingApp()
	matcher := newDeclaredRoutesMatcher(f.store)
	assertContractMatch(t, matcher, t.Context(), app, "/explicit", true)
	assertContractMatch(t, matcher, t.Context(), app, "/unused", false)
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), app, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err != nil {
		t.Fatalf("unused blocked document affected explicit contract: %v", err)
	}
}

func TestPublicDeclaredContractPostgresBoundsOutageOwnershipAndCleanup(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:bounded-contract")
	f.importedContract(t, "/allowed")
	app := f.routingApp()
	inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "production"}
	lock, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(t.Context(), `LOCK TABLE app_openapi_docs IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := newPublicRoutingPinner(f.store)(bounded, app, inputs); err == nil {
		t.Fatal("unavailable contract authorized dispatch")
	}
	if err := lock.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), app, inputs); err != nil {
		t.Fatalf("contract store recovery failed: %v", err)
	}
	var escaped state.PublicHostPolicyReader
	if err := f.store.WithPublicHostPolicySnapshot(t.Context(), func(reader state.PublicHostPolicyReader) error {
		escaped = reader
		if _, err := reader.PublicHostOpenAPIDoc(t.Context(), f.app.ID, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("document crossed owner: %v", err)
		}
		if _, err := reader.PublicHostRoutePolicy(t.Context(), uuid.NewString(), f.app.ID, "staging"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("scoped contract crossed owner: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := escaped.PublicHostOpenAPIDoc(t.Context(), f.app.ID, f.app.AccountID); err == nil {
		t.Fatal("document reader retained its transaction")
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE app_openapi_docs SET doc=jsonb_build_object('padding',repeat('x',$2::integer)) WHERE app_id=$1`, f.app.ID, api.TrafficPolicyMaxContractBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := f.store.WithPublicHostPolicySnapshot(t.Context(), func(reader state.PublicHostPolicyReader) error {
		doc, err := reader.PublicHostOpenAPIDoc(t.Context(), f.app.ID, f.app.AccountID)
		if err == nil || len(doc) != 0 {
			t.Fatalf("oversized contract transferred: len=%d err=%v", len(doc), err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), app, inputs); err == nil {
		t.Fatal("oversized contract authorized old dispatch")
	}
	f.importedContract(t, "/repaired")
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), f.routingApp(), inputs); err != nil {
		t.Fatal(err)
	}
}

func TestImportedSnapshotCopiesDocumentAndRefusesMissingCarrier(t *testing.T) {
	matcher := newDeclaredRoutesMatcher(nil)
	document := []byte(`{"openapi":"3.0.0","paths":{"/old":{"get":{}}}}`)
	app := gateway.App{ID: "app", AccountID: "owner", PublicPolicySource: &gateway.PublicAppPolicySource{Revision: "verified"},
		ImportedRoutePolicy: &gateway.ImportedRoutePolicy{Found: true, Document: document}}
	ctx, resolved, _, err := matcher.PinDeclaredRoutePolicy(t.Context(), app)
	if err != nil {
		t.Fatal(err)
	}
	copy(document, strings.Repeat("x", len(document)))
	if string(resolved.ImportedRoutePolicy.Document) == string(document) {
		t.Fatal("admitted document retained mutable source bytes")
	}
	assertContractMatch(t, matcher, ctx, resolved, "/old", true)
	encoded, err := json.Marshal(resolved)
	if err != nil || strings.Contains(string(encoded), "openapi") || strings.Contains(string(encoded), "/old") {
		t.Fatalf("raw imported document entered serialized app evidence: %v", err)
	}
	app.ImportedRoutePolicy = nil
	if _, _, _, err := matcher.PinDeclaredRoutePolicy(t.Context(), app); err == nil {
		t.Fatal("production policy loaded an unverified second contract")
	}
}

func TestPublicDeclaredContractPostgresTwoHTTPGatewaysRepairWithoutNotify(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	deployment := f.deployment(t, "production", "sha256:contract-http")
	f.importedContract(t, "/old")
	if _, err := f.pool.Exec(t.Context(), `UPDATE apps SET only_declared_routes=true WHERE id=$1`, f.app.ID); err != nil {
		t.Fatal(err)
	}
	type peer struct {
		server   *httptest.Server
		forwards *atomic.Int32
		before   string
	}
	peers := make([]peer, 0, 2)
	for _, h2 := range []bool{false, true} {
		router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
		scheduler := gateway.NewFakeScheduler("node").WithInstanceID(uuid.NewString()).WithDeploymentID(deployment.ID)
		backend := gateway.NewPGBackend(router, scheduler, nil)
		handler := gateway.NewHandlerWith(backend, nil, nil)
		handler.SetWakeGateHook()
		handler.WithDeclaredRouteMatcher(newDeclaredRoutesMatcher(f.store)).
			WithPublicRoutingPolicy(newPublicRoutingPinner(f.store)).
			WithEdgeRules(newGatewaydEdgeRules(f.store, nil, nil, nil), nil, nil)
		forwards := &atomic.Int32{}
		handler.WithForwarding(func(target gateway.Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if target.DeploymentID != deployment.ID {
					t.Errorf("contract request changed deployment: %s", target.DeploymentID)
				}
				forwards.Add(1)
				w.WriteHeader(http.StatusNoContent)
			})
		})
		server := httptest.NewUnstartedServer(handler)
		server.EnableHTTP2 = h2
		if h2 {
			server.StartTLS()
		} else {
			server.Start()
		}
		t.Cleanup(server.Close)
		server.Client().Timeout = 2 * time.Second
		peers = append(peers, peer{server: server, forwards: forwards})
	}
	request := func(server *httptest.Server, route string, status int) string {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, server.URL+route, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = f.app.Slug + ".apps.gregale.dev"
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != status {
			t.Fatalf("HTTP contract %s: status=%d err=%v body=%s", route, res.StatusCode, err, body)
		}
		return res.Header.Get(gateway.TrafficPolicyRevisionHeader)
	}
	for index := range peers {
		peers[index].before = request(peers[index].server, "/old", http.StatusNoContent)
		if peers[index].before == "" || peers[index].forwards.Load() != 1 {
			t.Fatal("old contract did not warm the production handler")
		}
	}
	f.importedContract(t, "/new") // Deliberately omit every cache invalidation and notification.
	for _, peer := range peers {
		request(peer.server, "/old", http.StatusNotFound)
		if peer.forwards.Load() != 1 {
			t.Fatal("obsolete declared route reached forwarding")
		}
		after := request(peer.server, "/new", http.StatusNoContent)
		if after == "" || after == peer.before || peer.forwards.Load() != 2 {
			t.Fatal("fresh contract/evidence did not reach both gateways")
		}
	}
}
