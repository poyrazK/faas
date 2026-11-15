package granttest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type PublicationFixture struct {
	Base                  *Fixture
	Owner, Publisher      *api.Client
	PublisherKey          state.APIKey
	Publication           api.ToolPublicationRecord
	Candidate             state.Deployment
	Request               api.ToolPublicationVerificationRequest
	Native, Runtime       *httptest.Server
	Identity              *age.X25519Identity
	Token                 string
	Credential            atomic.Value
	CatalogReflection     atomic.Value
	Calls                 atomic.Int32
	Mode                  atomic.Int32 // 1: fixture failure, 2: catalog credential reflection, 3: protocol failure
	LoseVerificationReply atomic.Bool
}

func NewPublication(t *testing.T, store state.Store, configure ...func(*toolregistry.PrivateExecutionOptions)) *PublicationFixture {
	t.Helper()
	f := &PublicationFixture{Base: New(t, store, ""), Token: "publication-provider-access-token"}
	f.Credential.Store(f.Token)
	f.CatalogReflection.Store("")
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	f.Identity = identity
	f.RotateTestToken(t, f.Token)
	f.Runtime = httptest.NewTLSServer(http.HandlerFunc(f.serveRuntime))
	t.Cleanup(f.Runtime.Close)
	f.Base.PreviewURL = f.Runtime.URL
	overrides, _ := json.Marshal(map[string]string{"MCP_CATALOG_EXPECTED_SHA256": strings.Repeat("c", 64)})
	f.Candidate, err = store.CreateDeployment(t.Context(), state.Deployment{AppID: f.Base.AppA.ID, Kind: state.DeploymentKindTarball, Status: state.DeployLive, TrafficPercentExplicit: true, SourceSHA256: strings.Repeat("9", 64), OverrideEnv: overrides})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateDeploymentStatus(t.Context(), f.Candidate.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	publisherToken, publisherHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	f.PublisherKey, err = store.CreateAPIKey(t.Context(), f.Base.Account.ID, publisherHash, "publisher", []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := store.ListAPIKeys(t.Context(), f.Base.Account.ID)
	if err != nil {
		t.Fatal(err)
	}
	var owner state.APIKey
	for _, key := range keys {
		if key.Label == "owner" {
			owner = key
		}
	}
	opts := toolregistry.PrivateExecutionOptions{Enabled: true, PublicationEnabled: true,
		Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{identity} },
		Endpoint:   func(context.Context, state.App, state.Deployment) (string, error) { return f.Runtime.URL, nil },
		VerifyPin:  func(context.Context, api.ToolInvocationTarget, string) error { return nil }, HTTP: f.Runtime.Client(),
		PublicationProtocols: func(ctx context.Context, _ api.ToolInvocationTarget, endpoint, token string, contract mcphosting.Contract, legacy bool, wrap func(http.RoundTripper) http.RoundTripper) error {
			if f.Mode.Load() == 3 {
				return state.ErrConflict
			}
			versions := []string{mcphosting.ProtocolVersion}
			if legacy {
				versions = append(versions, mcphosting.LegacyProtocolVersion)
			}
			for _, version := range versions {
				probe, err := mcphosting.NewClient(endpoint, token, version)
				if err != nil {
					return err
				}
				client := *f.Runtime.Client()
				client.Transport = wrap(client.Transport)
				probe.HTTP = &client
				baseline := contract
				baseline.ProtocolVersion = version
				if !mcphosting.CheckReadiness(ctx, probe, &baseline).OK {
					return state.ErrConflict
				}
			}
			return nil
		}}
	for _, apply := range configure {
		apply(&opts)
	}
	e := toolregistry.NewPrivateExecutor(store, opts)
	f.Base.PrivateExecutor = e
	t.Cleanup(e.Close)
	wrap := func(h func(http.ResponseWriter, *http.Request, state.Account)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var key state.APIKey
			switch strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") {
			case publisherToken:
				key = f.PublisherKey
			case f.Base.Owner.Token():
				key = owner
			case f.Base.AgentToken:
				key = f.Base.AgentKey
			default:
				w.WriteHeader(401)
				return
			}
			r = r.WithContext(middleware.WithPrincipal(r.Context(), f.Base.Account, &key, nil))
			h(w, r, f.Base.Account)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/tool-publications/{id}", wrap(toolregistry.Handler(store, true)))
	mux.HandleFunc("POST /v1/tool-publications/{id}/outcome", wrap(toolregistry.Handler(store, true)))
	mux.HandleFunc("/v1/tool-publications/{id}/verification", wrap(func(w http.ResponseWriter, r *http.Request, a state.Account) {
		if r.Method == http.MethodPost && f.LoseVerificationReply.CompareAndSwap(true, false) {
			rec := httptest.NewRecorder()
			e.PublicationVerification()(rec, r, a)
			http.Error(w, "lost verification reply after native commit", http.StatusServiceUnavailable)
			return
		}
		e.PublicationVerification()(w, r, a)
	}))
	mux.Handle("/", f.Base.Native.Config.Handler)
	f.Native = httptest.NewServer(mux)
	t.Cleanup(f.Native.Close)
	f.Owner = api.NewClient(f.Native.URL, f.Base.Owner.Token())
	f.Publisher = api.NewClient(f.Native.URL, publisherToken)
	raw := `{"version":1,"cases":[{"name":"works","kind":"tool","target":"visible","arguments":{"value":1},"expected_result":{"content":[{"type":"text","text":"ok"}]}}]}`
	f.Request = api.ToolPublicationVerificationRequest{DeploymentID: f.Candidate.ID, CatalogSHA256: strings.Repeat("c", 64), FixturesJSON: raw}
	serving := ""
	current, err := store.DeploymentByID(t.Context(), f.Base.PublicationA.Outcome.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	if current.TrafficPercent > 0 {
		serving = current.ID
	}
	in := api.ToolPublicationIntent{ExpectedServingDeploymentID: serving, App: f.Base.AppA.Slug, AppID: f.Base.AppA.ID, SourceSHA256: strings.Repeat("8", 64), FixturesSHA256: toolpublication.Digest([]byte(raw)), AccessMode: "external-oauth", EndpointPath: "/mcp", ToolScopes: map[string][]string{"visible": {"tool:read"}}, TestCases: []string{"works"}, PlatformVerification: true, Legacy: true, Auth: &api.ToolPublicationAuth{Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/jwks", Resource: "https://private.example/mcp", Scopes: []string{"tool:read"}}}
	out, err := f.Publisher.BeginToolPublication(t.Context(), strings.ReplaceAll(uuid.NewString(), "-", "")+strings.ReplaceAll(uuid.NewString(), "-", ""), in)
	if err != nil {
		t.Fatal(err)
	}
	f.Publication = out.Record
	return f
}

func (f *PublicationFixture) RotateTestToken(t *testing.T, token string) {
	t.Helper()
	ciphertext, err := secretbox.SealOne(f.Identity.Recipient(), "MCP_PUBLICATION_TEST", token, api.MCPCatalogOAuthMaxTokenBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Base.Store.UpsertAppSecretWithKidInScope(t.Context(), f.Base.Account.ID, f.Base.AppA.ID, api.DefaultEnvScope, "MCP_PUBLICATION_TEST", f.Identity.Recipient().String(), ciphertext); err != nil {
		t.Fatal(err)
	}
}

func (f *PublicationFixture) serveRuntime(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": "https://private.example/mcp", "authorization_servers": []string{"https://issuer.example"}, "scopes_supported": []string{"tool:read"}})
		return
	}
	if r.Header.Get("Origin") != "" {
		w.WriteHeader(403)
		return
	}
	token := f.Credential.Load().(string)
	if r.Header.Get("Authorization") != "Bearer "+token {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://private.example/.well-known/oauth-protected-resource/mcp"`)
		w.WriteHeader(401)
		return
	}
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		w.WriteHeader(400)
		return
	}
	if request.Method == "notifications/initialized" {
		w.WriteHeader(202)
		return
	}
	w.Header().Set("MCP-Protocol-Version", r.Header.Get("MCP-Protocol-Version"))
	w.Header().Set("Content-Type", "application/json")
	var result any = map[string]any{}
	switch request.Method {
	case "server/discover":
		result = map[string]any{"supportedVersions": []string{mcphosting.ProtocolVersion}, "capabilities": map[string]any{"tools": map[string]any{}}}
	case "initialize":
		result = map[string]any{"protocolVersion": r.Header.Get("MCP-Protocol-Version"), "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "private-fixture", "version": "1"}}
	case "tools/list":
		description := "reviewed tool"
		if f.Mode.Load() == 2 {
			description = token
		}
		if reflected := f.CatalogReflection.Load().(string); reflected != "" {
			description = reflected
		}
		result = map[string]any{"tools": []mcphosting.Tool{{Name: "visible", Description: description, InputSchema: map[string]any{"type": "object"}}}}
	case "tools/call":
		f.Calls.Add(1)
		text := "ok"
		if f.Mode.Load() == 1 {
			text = "failed"
		}
		result = map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
}

func (f *PublicationFixture) Approve(t *testing.T) api.ToolPublicationVerification {
	t.Helper()
	v, err := f.Publisher.VerifyToolPublication(t.Context(), f.Publication.ID, f.Request)
	if err != nil {
		t.Fatal(err)
	}
	v, err = f.Owner.DecideToolPublicationVerification(t.Context(), f.Publication.ID, api.ToolPublicationVerificationDecision{RequestSHA256: v.RequestSHA256, CredentialSecretRef: "secret:MCP_PUBLICATION_TEST"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *PublicationFixture) Wait(t *testing.T, want string) api.ToolPublicationVerification {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		v, err := f.Publisher.VerifyToolPublication(t.Context(), f.Publication.ID, f.Request)
		if err != nil {
			t.Fatal(err)
		}
		if v.Status == want {
			return v
		}
		if v.Status != "approved" && v.Status != "running" {
			auth := f.Publication.Intent.Auth
			probe, _ := mcphosting.NewClient(f.Runtime.URL+"/mcp", f.Token, mcphosting.ProtocolVersion)
			probe.HTTP = f.Runtime.Client()
			probe.ExpectedAuth = &mcphosting.AuthConfig{Mode: "external-oauth", Issuer: auth.Issuer, JWKSURL: auth.JWKSURL, Resource: auth.Resource, Scopes: auth.Scopes}
			diagnostic := mcphosting.Doctor(t.Context(), probe, true, "", nil)
			t.Fatalf("verification ended %s; want %s; outcome=%+v fixture checks=%+v", v.Status, want, v.Outcome, diagnostic.Checks)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("verification did not finish")
	return api.ToolPublicationVerification{}
}
