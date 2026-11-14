// Package invocationtest provides portable native API fixtures for invocation
// tests. It uses the real account-scoped handlers and stores, not tool execution.
package invocationtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolinvocation"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type Fixture struct {
	Store        state.Store
	Account      state.Account
	App          state.App
	Deployment   state.Deployment
	Publication  api.ToolPublicationRecord
	Client       *api.Client
	Native       *httptest.Server
	NativeToken  string
	PreviewURL   string
	PreviewHost  string
	LoseDispatch bool
	LoseBegin    bool
}

func New(t *testing.T, store state.Store, access string) *Fixture {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "tool-invocation@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "invocation-tools", Type: state.AppTypeApp, RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	pin := strings.Repeat("d", 64)
	override, _ := json.Marshal(map[string]string{"MCP_CATALOG_EXPECTED_SHA256": pin})
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourceSHA256: strings.Repeat("e", 64), SourceBytes: 100, SourcePath: "/private/fixture.tar.gz", Status: state.DeployLive, TrafficPercent: 100, OverrideEnv: override})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	dep, err = store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	definition := mcphosting.Tool{Name: "echo", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "integer"}}, "required": []string{"value"}, "additionalProperties": false}}
	catalog, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: []mcphosting.Tool{definition}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := mcphosting.MarshalContract(catalog)
	intent := api.ToolPublicationIntent{App: app.Slug, AppID: app.ID, SourceSHA256: strings.Repeat("b", 64), FixturesSHA256: strings.Repeat("c", 64), EndpointPath: "/mcp", AccessMode: access, ToolScopes: map[string][]string{"echo": {}}, TestCases: []string{"echoes reviewed input"}}
	journal := store.(state.ToolPublicationStore)
	version := strings.Repeat("a", 64)
	if _, err = journal.BeginToolPublication(ctx, account.ID, version, intent); err != nil {
		t.Fatal(err)
	}
	outcome := api.ToolPublicationOutcome{Status: "completed", DeploymentID: dep.ID, CatalogSHA256: pin, Catalog: raw, ProtocolVerified: true, Checks: []api.ToolPublicationCheck{{Name: intent.TestCases[0], Status: "passed"}}}
	publication, err := journal.FinishToolPublication(ctx, account.ID, version, outcome, dep.SourceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	f := &Fixture{Store: store, Account: account, App: app, Deployment: dep, Publication: publication, NativeToken: "platform-native-credential"}
	f.Native = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Native.Close)
	f.Client = api.NewClient(f.Native.URL, f.NativeToken)
	return f
}

func (f *Fixture) Intent(raw []byte) api.ToolInvocationIntent {
	canonical, _ := toolinvocation.CanonicalArguments(raw)
	return api.ToolInvocationIntent{Version: f.Publication.ID, Name: "echo", ArgumentsSHA256: toolpublication.Digest(canonical), AccessMode: f.Publication.Intent.AccessMode, TimeoutSeconds: 10, AllowToolExecution: true}
}

func (f *Fixture) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+f.NativeToken {
		w.WriteHeader(401)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	key := &state.APIKey{AccountID: f.Account.ID, Scopes: []string{api.ScopeAdmin}}
	r = r.WithContext(middleware.WithPrincipal(r.Context(), f.Account, key, nil))
	encode := func(value any) { _ = json.NewEncoder(w).Encode(value) }
	switch {
	case r.URL.Path == "/v1/account":
		encode(api.AccountResponse{ID: f.Account.ID})
	case r.URL.Path == "/v1/apps/"+f.App.Slug:
		encode(api.AppResponse{ID: f.App.ID, Slug: f.App.Slug, URL: "https://invocation-tools.example"})
	case r.URL.Path == "/v1/deployments/"+f.Deployment.ID:
		dep, err := f.Store.DeploymentByID(r.Context(), f.Deployment.ID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		encode(api.DeploymentResponse{ID: dep.ID, AppID: dep.AppID, SourceSHA256: dep.SourceSHA256, Scope: dep.Scope, Status: string(dep.Status), OverrideEnvKeys: []string{"MCP_CATALOG_EXPECTED_SHA256"}})
	case r.URL.Path == "/v1/deployments/"+f.Deployment.ID+"/url":
		encode(api.DeploymentPreviewURL{DeploymentID: f.Deployment.ID, AppID: f.App.ID, URL: f.PreviewURL, Host: f.PreviewHost, Alive: true})
	case len(parts) >= 3 && parts[1] == "tool-publications":
		r.SetPathValue("id", parts[2])
		toolregistry.Handler(f.Store)(w, r, f.Account)
	case len(parts) >= 3 && parts[1] == "tool-invocations":
		r.SetPathValue("id", parts[2])
		if len(parts) > 3 {
			r.SetPathValue("action", parts[3])
		}
		if f.LoseDispatch && r.PathValue("action") == "dispatch" || f.LoseBegin && r.Method == http.MethodPut {
			capture := httptest.NewRecorder()
			toolregistry.Invocations(f.Store)(capture, r, f.Account)
			if capture.Code == 200 {
				http.Error(w, "lost receipt after commit", http.StatusServiceUnavailable)
				return
			}
			for name, values := range capture.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(capture.Code)
			_, _ = w.Write(capture.Body.Bytes())
			return
		}
		toolregistry.Invocations(f.Store)(w, r, f.Account)
	default:
		http.NotFound(w, r)
	}
}
