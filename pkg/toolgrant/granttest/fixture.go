// Package granttest supplies real memory/PostgreSQL-backed native handlers for
// portable delegation journeys. Authentication deliberately retains key copies
// so handler tests also exercise revocation after an authentication cache hit.
package granttest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
)

type Fixture struct {
	LosePrivateReply           atomic.Bool
	Store                      state.Store
	Account                    state.Account
	ProjectA, ProjectB         state.Project
	AppA, AppB                 state.App
	PublicationA, PublicationB api.ToolPublicationRecord
	Owner, Agent, Other        *api.Client
	AgentKey, OtherKey         state.APIKey
	AgentToken                 string
	Native                     *httptest.Server
	PrivateExecutor            *toolregistry.PrivateExecutor
	PreviewURL                 string
	LoseBegin                  bool
	LoseDispatch               bool
}

func New(t *testing.T, store state.Store, agentToken string, private ...bool) *Fixture {
	t.Helper()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "tool-grants@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f := &Fixture{Store: store, Account: acct}
	f.ProjectA, err = store.CreateProject(ctx, state.Project{ID: uuid.NewString(), AccountID: acct.ID, Slug: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	f.ProjectB, err = store.CreateProject(ctx, state.Project{ID: uuid.NewString(), AccountID: acct.ID, Slug: "beta"})
	if err != nil {
		t.Fatal(err)
	}
	access := []string{}
	if len(private) > 0 && private[0] {
		access = []string{"external-oauth"}
	}
	f.AppA, f.PublicationA = Publication(t, store, acct, "alpha-tools", f.ProjectA.ID, strings.Repeat("a", 64), access...)
	f.AppB, f.PublicationB = Publication(t, store, acct, "beta-tools", f.ProjectB.ID, strings.Repeat("b", 64))
	ownerToken, ownerHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.CreateAPIKey(ctx, acct.ID, ownerHash, "owner", api.ScopesAdminOnly)
	if err != nil {
		t.Fatal(err)
	}
	if agentToken == "" {
		agentToken = "fp_grant_fixture_agent_" + uuid.NewString()
	}
	f.AgentToken = agentToken
	f.AgentKey, err = store.CreateAPIKey(ctx, acct.ID, api.HashToken([]byte(agentToken)), "agent", []string{api.ScopeAgent, api.ScopeAppsRead})
	if err != nil {
		t.Fatal(err)
	}
	limiter, ok := store.(interface {
		SetAPIKeyProjectLimit(context.Context, string, []string) error
	})
	if !ok {
		t.Fatal("project limits unavailable")
	}
	if err = limiter.SetAPIKeyProjectLimit(ctx, f.AgentKey.ID, []string{f.ProjectA.ID}); err != nil {
		t.Fatal(err)
	}
	otherToken, otherHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	f.OtherKey, err = store.CreateAPIKey(ctx, acct.ID, otherHash, api.MCPLoginKeyLabel, []string{api.ScopeAppsRead})
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]state.APIKey{ownerToken: owner, agentToken: f.AgentKey, otherToken: f.OtherKey}
	mux := http.NewServeMux()
	wrap := func(h func(http.ResponseWriter, *http.Request, state.Account)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			key, ok := keys[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
			if !ok {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			r = r.WithContext(middleware.WithPrincipal(r.Context(), acct, &key, nil))
			h(w, r, acct)
		}
	}
	mux.HandleFunc("GET /v1/account", wrap(func(w http.ResponseWriter, _ *http.Request, _ state.Account) {
		_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: acct.ID})
	}))
	mux.HandleFunc("GET /v1/tool-registry", wrap(toolregistry.Search(store)))
	mux.HandleFunc("GET /v1/tool-registry/{version}/tools/{name}", wrap(toolregistry.Describe(store)))
	mux.HandleFunc("/v1/tool-grants", wrap(toolregistry.Grants(store, nil)))
	mux.HandleFunc("/v1/tool-grants/{id}", wrap(toolregistry.Grants(store, nil)))
	mux.HandleFunc("/v1/tool-oauth-connections", wrap(toolregistry.Connections(store, nil)))
	mux.HandleFunc("/v1/tool-oauth-connections/{id}", wrap(toolregistry.Connections(store, nil)))
	mux.HandleFunc("/v1/tool-oauth-connections/{id}/authorization", wrap(func(w http.ResponseWriter, r *http.Request, a state.Account) {
		if f.PrivateExecutor == nil {
			http.Error(w, "executor unavailable", http.StatusServiceUnavailable)
			return
		}
		f.PrivateExecutor.AuthorizationHandler()(w, r, a)
	}))
	mux.HandleFunc("/v1/delegated-tool-connections/{grant}", wrap(toolregistry.DelegatedConnection(store)))
	mux.HandleFunc("/v1/tool-publications/{id}", wrap(toolregistry.Handler(store)))
	mux.HandleFunc("/v1/tool-invocations/{id}", wrap(toolregistry.Invocations(store)))
	mux.HandleFunc("/v1/tool-invocations/{id}/{action}", wrap(toolregistry.Invocations(store)))
	delegated := toolregistry.DelegatedInvocations(store, nil)
	callDelegated := func(w http.ResponseWriter, r *http.Request, account state.Account) {
		if r.PathValue("action") == "execute" && f.PrivateExecutor != nil {
			capture := httptest.NewRecorder()
			f.PrivateExecutor.Handler()(capture, r, account)
			if capture.Code == http.StatusOK && f.LosePrivateReply.CompareAndSwap(true, false) {
				http.Error(w, "lost private receipt after commit", http.StatusServiceUnavailable)
				return
			}
			for name, values := range capture.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(capture.Code)
			_, _ = w.Write(capture.Body.Bytes())
			return
		}
		if f.LoseBegin && r.Method == http.MethodPut || f.LoseDispatch && r.PathValue("action") == "dispatch" {
			capture := httptest.NewRecorder()
			delegated(capture, r, account)
			if capture.Code == http.StatusOK {
				http.Error(w, "receipt lost after commit", http.StatusServiceUnavailable)
				return
			}
			for name, values := range capture.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(capture.Code)
			_, _ = w.Write(capture.Body.Bytes())
			return
		}
		delegated(w, r, account)
	}
	mux.HandleFunc("/v1/delegated-tool-invocations/{id}", wrap(callDelegated))
	mux.HandleFunc("/v1/delegated-tool-invocations/{id}/{action}", wrap(callDelegated))
	mux.HandleFunc("GET /v1/apps/{slug}", wrap(func(w http.ResponseWriter, r *http.Request, _ state.Account) {
		app, err := store.AppBySlug(r.Context(), r.PathValue("slug"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(api.AppResponse{ID: app.ID, Slug: app.Slug, ProjectID: app.ProjectID})
	}))
	mux.HandleFunc("GET /v1/deployments/{id}", wrap(func(w http.ResponseWriter, r *http.Request, _ state.Account) {
		dep, err := store.DeploymentByID(r.Context(), r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: dep.ID, AppID: dep.AppID, Status: string(dep.Status), SourceSHA256: dep.SourceSHA256, Scope: dep.Scope})
	}))
	mux.HandleFunc("GET /v1/deployments/{id}/url", wrap(func(w http.ResponseWriter, r *http.Request, _ state.Account) {
		dep, err := store.DeploymentByID(r.Context(), r.PathValue("id"))
		if err != nil || f.PreviewURL == "" {
			http.NotFound(w, r)
			return
		}
		u, err := url.Parse(f.PreviewURL)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(api.DeploymentPreviewURL{DeploymentID: dep.ID, AppID: dep.AppID, Alive: true, URL: f.PreviewURL, Host: u.Host})
	}))
	f.Native = httptest.NewServer(mux)
	t.Cleanup(f.Native.Close)
	f.Owner = api.NewClient(f.Native.URL, ownerToken)
	f.Agent = api.NewClient(f.Native.URL, agentToken)
	f.Other = api.NewClient(f.Native.URL, otherToken)
	return f
}

func Publication(t *testing.T, store state.Store, acct state.Account, slug, project, version string, access ...string) (state.App, api.ToolPublicationRecord) {
	t.Helper()
	ctx := t.Context()
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, ProjectID: project, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	overrides, _ := json.Marshal(map[string]string{"MCP_CATALOG_EXPECTED_SHA256": strings.Repeat("c", 64)})
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployLive, SourceSHA256: strings.Repeat("d", 64), OverrideEnv: overrides})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	in := api.ToolPublicationIntent{App: slug, AppID: app.ID, SourceSHA256: strings.Repeat("e", 64), FixturesSHA256: strings.Repeat("f", 64), AccessMode: "open", EndpointPath: "/mcp", ToolScopes: map[string][]string{"visible": {}, "hidden": {}}, TestCases: []string{"private-fixture-description"}}
	if len(access) > 0 {
		in.AccessMode = access[0]
		in.ToolScopes["visible"] = []string{"tool:read"}
	}
	c, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: []mcphosting.Tool{{Name: "visible", InputSchema: map[string]any{"type": "object"}}, {Name: "hidden", Description: "never-expose-hidden-schema", InputSchema: map[string]any{"type": "object"}}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := mcphosting.MarshalContract(c)
	if err != nil {
		t.Fatal(err)
	}
	journal := store.(state.ToolPublicationStore)
	if _, err = journal.BeginToolPublication(ctx, acct.ID, version, in); err != nil {
		t.Fatal(err)
	}
	p, err := journal.FinishToolPublication(ctx, acct.ID, version, api.ToolPublicationOutcome{Status: "completed", DeploymentID: dep.ID, CatalogSHA256: strings.Repeat("c", 64), Catalog: raw, ProtocolVerified: true, Checks: []api.ToolPublicationCheck{{Name: in.TestCases[0], Status: "passed"}}}, dep.SourceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	return app, p
}
