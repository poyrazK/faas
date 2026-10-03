package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestDevBridgeGatewayRouting(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "bridge-routing@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "development"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "payments", Status: state.AppActive, Visibility: api.AppVisibilityInternal})
	if err != nil {
		t.Fatal(err)
	}
	dependency, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "orders", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	session, credentials, err := devbridge.NewSession(devbridge.Scope{AccountID: account.ID, DeveloperID: "alice", ProjectID: project.ID, EnvironmentID: env.ID, TargetAppID: app.ID, DependencyAppIDs: []string{dependency.ID}}, time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDevBridge(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	authorize := developmentBridgeAuthorization(store)
	req := httptest.NewRequest("POST", "http://"+gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, env.ID, app.ID)+"/charge?currency=EUR", strings.NewReader("payment"))
	req.Header.Set(devbridge.SessionHeader, session.ID)
	req.Header.Set(devbridge.AccountHeader, account.ID)
	req.Header.Set(devbridge.TokenHeader, credentials.RequestToken)
	req.Header.Set("Authorization", "Bearer application-credential")
	if problem := authorize(req); problem != nil {
		t.Fatalf("valid bridge: %+v", problem)
	}
	if !gateway.DevBridgeAllowsPrivateEnvironment(req.Context(), account.ID, env.ID, app.ID) {
		t.Fatal("verified private environment not admitted")
	}
	if _, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "development", Status: state.DeployLive, ImageDigest: "sha256:development"}); err != nil {
		t.Fatal(err)
	}
	router := pgRouter{store: store, appsSuffix: wire.DeployWildcardSuffix, deploySuffix: wire.DeployWildcardSuffix}
	if resolved, ok, err := router.ResolveHost(req.Context(), req.Host); err != nil || !ok || resolved.ID != app.ID {
		t.Fatalf("private bridge resolution: ok=%v err=%v", ok, err)
	}
	if _, ok, err := router.ResolveHost(t.Context(), req.Host); err != nil || ok {
		t.Fatalf("private environment exposed without session: ok=%v err=%v", ok, err)
	}
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/dev/bridges/"+session.ID+"/traffic/charge" || r.URL.RawQuery != "currency=EUR" {
			t.Errorf("wrong relay URL: %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer application-credential" {
			t.Error("application authentication lost")
		}
		w.WriteHeader(202)
	}))
	defer relay.Close()
	target, _ := url.Parse(relay.URL)
	forward := developmentBridgeForwarder(store, target)
	rec := httptest.NewRecorder()
	if !forward(rec, req, gateway.App{ID: app.ID, AccountID: account.ID}) || rec.Code != 202 {
		t.Fatalf("forward: %d", rec.Code)
	}
	ordinary := httptest.NewRequest("GET", req.URL.String(), nil)
	if authorize(ordinary) != nil || forward(httptest.NewRecorder(), ordinary, gateway.App{ID: app.ID, AccountID: account.ID}) {
		t.Fatal("ordinary traffic intercepted")
	}
	depReq := httptest.NewRequest("GET", "http://"+gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, env.ID, dependency.ID)+"/orders", nil)
	depReq.Header.Set(devbridge.SessionHeader, session.ID)
	depReq.Header.Set(devbridge.AccountHeader, account.ID)
	depReq.Header.Set(devbridge.TokenHeader, credentials.AttachmentToken)
	if problem := authorize(depReq); problem != nil {
		t.Fatalf("dependency rejected: %+v", problem)
	}
	if forward(httptest.NewRecorder(), depReq, gateway.App{ID: dependency.ID, AccountID: account.ID}) {
		t.Fatal("dependency call intercepted locally")
	}
	if depReq.Header.Get(devbridge.TokenHeader) != "" {
		t.Fatal("attachment token would reach remote guest")
	}
	req.Header.Set(devbridge.TokenHeader, "forged")
	if authorize(req) == nil {
		t.Fatal("forged session accepted")
	}
	req.Header.Set(devbridge.TokenHeader, credentials.RequestToken)
	if err := store.RevokeDevBridge(t.Context(), account.ID, session.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if authorize(req) == nil {
		t.Fatal("revoked session accepted")
	}
}
