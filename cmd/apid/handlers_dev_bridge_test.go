package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestDevBridgeCreationInspectionRevocation(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.devBridgeEnabled = true
	project, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "development"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "payments", Type: state.AppTypeApp, RAMMB: 128, IdleTimeoutS: 30, WorkloadClass: state.WorkloadClassHTTP})
	if err != nil {
		t.Fatal(err)
	}
	request := api.CreateDevBridgeRequest{App: app.Slug, Environment: "development", DeveloperID: "alice"}
	response := e.do(t, "POST", "/v1/dev/bridges", request, nil)
	if response.Code != 201 {
		t.Fatalf("create: %d %s", response.Code, response.Body)
	}
	var out api.CreateDevBridgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Session.Scope.TargetAppID != app.ID || out.Credentials.AttachmentToken == "" {
		t.Fatal("missing scope or credentials")
	}
	inspection := e.do(t, "GET", "/v1/dev/bridges/"+out.Session.ID, nil, nil)
	if inspection.Code != 200 || strings.Contains(inspection.Body.String(), out.Credentials.RequestToken) || strings.Contains(inspection.Body.String(), out.Credentials.AttachmentToken) {
		t.Fatalf("inspection: %s", inspection.Body)
	}
	if got := e.do(t, "DELETE", "/v1/dev/bridges/"+out.Session.ID, nil, nil); got.Code != 204 {
		t.Fatalf("revoke: %s", got.Body)
	}
	session, err := e.store.DevBridgeByID(t.Context(), e.acct.ID, out.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(session.AuthorizeAttachment(time.Now(), out.Credentials.AttachmentToken), devbridge.ErrUnauthorized) {
		t.Fatal("revoked attachment accepted")
	}
	other, err := e.store.CreateAccount(t.Context(), "other-bridge@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DevBridgeByID(t.Context(), other.ID, out.Session.ID); err == nil {
		t.Fatal("cross-account inspection allowed")
	}
	if err := e.store.RevokeDevBridge(t.Context(), other.ID, out.Session.ID, time.Now()); err == nil {
		t.Fatal("cross-account revocation allowed")
	}
}

func TestDevBridgeRejectsProductionAndProtectedEnvironments(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.devBridgeEnabled = true
	project, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "payments", Type: state.AppTypeApp, RAMMB: 128, IdleTimeoutS: 30, WorkloadClass: state.WorkloadClassHTTP})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"production", "protected-dev"} {
		if name != "production" {
			_, err := e.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: name, Protected: true})
			if err != nil {
				t.Fatal(err)
			}
		}
		response := e.do(t, "POST", "/v1/dev/bridges", api.CreateDevBridgeRequest{App: app.Slug, Environment: name, DeveloperID: "alice"}, nil)
		if response.Code != 400 {
			t.Fatalf("%s: %d %s", name, response.Code, response.Body)
		}
	}
	e.s.devBridgeEnabled = false
	if response := e.do(t, "POST", "/v1/dev/bridges", nil, nil); response.Code != 503 {
		t.Fatalf("disabled: %d", response.Code)
	}
}

func TestDevBridgeEntrypointPreservesDiscoveredBindings(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.devBridgeEnabled = true
	project, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := e.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "development"})
	if err != nil {
		t.Fatal(err)
	}
	apps := map[string]state.App{}
	for _, name := range []string{"payments", "frontend", "inventory"} {
		app := state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: name, Type: state.AppTypeApp, RAMMB: 128, WorkloadClass: state.WorkloadClassHTTP}
		if name == "payments" {
			app.Manifest.ServiceBindings = []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_INVENTORY_URL", Service: "inventory"}}
		}
		apps[name], err = e.store.CreateApp(t.Context(), app)
		if err != nil {
			t.Fatal(err)
		}
	}
	response := e.do(t, "POST", "/v1/dev/bridges", api.CreateDevBridgeRequest{App: "payments", Environment: env.Slug, DeveloperID: "alice", Entrypoint: "frontend"}, nil)
	if response.Code != 201 {
		t.Fatalf("create: %d %s", response.Code, response.Body)
	}
	var out api.CreateDevBridgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.EnvironmentURL != "https://"+gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, env.ID, apps["frontend"].ID) {
		t.Fatalf("wrong entrypoint: %s", out.EnvironmentURL)
	}
	deps := map[string]bool{}
	for _, dep := range out.Dependencies {
		deps[dep.AppID] = true
		if dep.EnvironmentURL == "" {
			t.Fatal("missing discovery URL")
		}
	}
	if len(deps) != 2 || !deps[apps["frontend"].ID] || !deps[apps["inventory"].ID] {
		t.Fatalf("lost graph: %+v", deps)
	}
	response = e.do(t, "POST", "/v1/dev/bridges", api.CreateDevBridgeRequest{App: "payments", Environment: env.Slug, DeveloperID: "alice", Entrypoint: "outside-project"}, nil)
	if response.Code != 400 {
		t.Fatalf("invalid entrypoint admitted: %d", response.Code)
	}
}
