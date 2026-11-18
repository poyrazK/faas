package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	applicationResponseAppID         = "11111111-1111-4111-8111-111111111111"
	applicationResponseDeploymentID  = "22222222-2222-4222-8222-222222222222"
	applicationResponseInstanceID    = "33333333-3333-4333-8333-333333333333"
	applicationResponseProjectID     = "44444444-4444-4444-8444-444444444444"
	applicationResponseEnvironmentID = "55555555-5555-4555-8555-555555555555"
)

type applicationResponseFixture struct {
	t               *testing.T
	apiToken        string
	mu              sync.Mutex
	app             api.AppResponse
	deployment      api.DeploymentResponse
	environment     api.ProjectEnvironmentResponse
	capability      api.DeploymentResponseCheckCapabilities
	result          api.DeploymentResponseCheckResult
	path            string
	checkError      int
	dropConnection  bool
	capabilityError int
	checks          int
	requests        int
	afterCheck      string
	revoked         bool
	onCheck         func(context.Context)
}

func newApplicationResponseFixture(t *testing.T) *applicationResponseFixture {
	t.Helper()
	return &applicationResponseFixture{
		t:           t,
		apiToken:    testToken,
		app:         api.AppResponse{ID: applicationResponseAppID, Slug: "alpha", ProjectID: applicationResponseProjectID, RequireAuthn: true},
		deployment:  api.DeploymentResponse{ID: applicationResponseDeploymentID, AppID: applicationResponseAppID, Scope: "default", Status: "live"},
		environment: api.ProjectEnvironmentResponse{ID: applicationResponseEnvironmentID, ProjectID: applicationResponseProjectID, Slug: "staging"},
		capability:  api.DeploymentResponseCheckCapabilities{ProtocolVersion: api.DeploymentResponseCheckProtocolVersion, Available: true},
		result:      api.DeploymentResponseCheckResult{AppID: applicationResponseAppID, DeploymentID: applicationResponseDeploymentID, Scope: "default", InstanceID: applicationResponseInstanceID, HTTPStatus: 200, Body: "application response " + testToken},
		path:        "/ready?source=mcp",
	}
}

func (f *applicationResponseFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer "+f.apiToken {
		f.t.Error("response check did not use the selected API credential")
	}
	if f.revoked {
		api.WriteProblem(w, api.NewProblem(403, "forbidden", "Revoked", "access revoked"))
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/alpha":
		_ = json.NewEncoder(w).Encode(f.app)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/deployments/") && r.URL.Path != "/v1/deployments/response-check/capabilities":
		_ = json.NewEncoder(w).Encode(f.deployment)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/deployments/response-check/capabilities":
		if f.capabilityError != 0 {
			api.WriteProblem(w, api.NewProblem(f.capabilityError, "capability_denied", "Denied", "deploy access required"))
			return
		}
		_ = json.NewEncoder(w).Encode(f.capability)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/shop":
		_ = json.NewEncoder(w).Encode(api.ProjectResponse{ProjectSummaryResponse: api.ProjectSummaryResponse{ID: applicationResponseProjectID, Slug: "shop"}, Workloads: []api.ProjectWorkloadResponse{{Slug: "alpha"}}})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/shop/environments/staging":
		_ = json.NewEncoder(w).Encode(f.environment)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/alpha/deployments/"+f.deployment.ID+"/response-check":
		f.checks++
		var request api.DeploymentResponseCheckRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.AppID != applicationResponseAppID || request.Scope != f.deployment.Scope || request.Path != f.path {
			f.t.Errorf("incorrect response-check target: %+v (%v)", request, err)
		}
		if request.Scope == "staging" && (request.ProjectID != applicationResponseProjectID || request.EnvironmentID != applicationResponseEnvironmentID) {
			f.t.Error("named response check lost catalog identities")
		}
		if f.onCheck != nil {
			f.onCheck(r.Context())
		}
		if f.dropConnection {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				f.t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		if f.checkError != 0 {
			api.WriteProblem(w, api.NewProblem(f.checkError, "capacity_unavailable", "Unavailable", "response check unavailable"))
			return
		}
		_ = json.NewEncoder(w).Encode(f.result)
		switch f.afterCheck {
		case "recreate":
			f.app.ID = applicationResponseInstanceID
		case "revoke":
			f.revoked = true
		case "recreate_environment":
			f.environment.ID = applicationResponseInstanceID
		}
	default:
		f.t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func applicationResponseArguments() map[string]any {
	return map[string]any{"app": "alpha", "deployment_id": applicationResponseDeploymentID, "path": "/ready?source=mcp"}
}

func TestApplicationResponseEvidenceAcrossMCPProtocols(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, status := range []int{200, 204, 302, 401, 500} {
			t.Run(version+"/"+http.StatusText(status), func(t *testing.T) {
				f := newApplicationResponseFixture(t)
				f.result.HTTPStatus, f.result.BodyTruncated = status, true
				backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
				defer backend.Close()
				cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true, Toolsets: []string{ToolsetCore}}, version)
				fields := callTool(t, ctx, cs, "gregale_verify_application_response", applicationResponseArguments(), false)
				raw, _ := json.Marshal(fields)
				var result applicationResponseResult
				if err := json.Unmarshal(raw, &result); err != nil || result.Verified != (status >= 200 && status < 300) || result.PublicAccessVerified || result.CheckedAt.IsZero() || result.SchemaVersion != 1 {
					t.Fatalf("verification evidence: %+v (%v)", result, err)
				}
				if result.AppID != applicationResponseAppID || result.DeploymentID != applicationResponseDeploymentID || result.InstanceID != applicationResponseInstanceID || result.HTTPStatus != status || result.Path != "/ready?source=mcp" || result.Body != "application response [REDACTED]" || !result.BodyTruncated {
					t.Fatalf("response evidence lost identity, status, body or redaction: %+v", result)
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				if f.checks != 1 {
					t.Fatalf("GET was repeated %d times", f.checks)
				}
			})
		}
	}
}

func TestApplicationResponseCatalogRequiresWrites(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		for _, set := range []string{ToolsetCore, ToolsetRelease} {
			for _, writes := range []bool{false, true} {
				backend := httptest.NewServer(http.NotFoundHandler())
				opts := Options{APIBase: backend.URL, AllowWrites: writes, Toolsets: []string{set}}
				if hosted {
					opts.Environments = &EnvironmentConsent{DefaultWrite: writes}
				}
				cs, ctx := connectSelectedTestServer(t, backend, opts, "2026-07-28")
				catalog, err := cs.ListTools(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				i := slices.IndexFunc(catalog.Tools, func(tool *mcp.Tool) bool { return tool.Name == "gregale_verify_application_response" })
				if (i >= 0) != writes {
					t.Fatalf("hosted=%v set=%s writes=%v: verification catalog mismatch", hosted, set, writes)
				}
				if i >= 0 && (catalog.Tools[i].Annotations.ReadOnlyHint || catalog.Tools[i].Annotations.IdempotentHint) {
					t.Fatal("active GET advertised as passive or repeat-safe")
				}
				backend.Close()
			}
		}
	}
}

func TestApplicationResponseRejectsInvalidInputsWithoutExecution(t *testing.T) {
	for _, change := range []map[string]any{
		{"deployment_id": "v1"}, {"deployment_id": "00000000-0000-0000-0000-000000000000"}, {"app": "../alpha"},
		{"path": "https://outside.example/"}, {"path": "//outside.example/"}, {"path": "/\\outside"}, {"path": "/#fragment"}, {"path": "/\n"}, {"path": "/" + strings.Repeat("a", api.DeploymentResponseCheckMaxPathBytes)},
	} {
		f := newApplicationResponseFixture(t)
		backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
		cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true}, "2026-07-28")
		args := applicationResponseArguments()
		for key, value := range change {
			args[key] = value
		}
		out := callTool(t, ctx, cs, "gregale_verify_application_response", args, true)
		if toolCode(t, out) != "invalid_argument" {
			t.Fatalf("invalid input: %s", out["code"])
		}
		f.mu.Lock()
		if f.requests != 0 {
			t.Error("invalid input reached the API")
		}
		f.mu.Unlock()
		backend.Close()
	}
}

func TestApplicationResponseRejectsUnboundEvidence(t *testing.T) {
	for name, change := range map[string]func(*api.DeploymentResponseCheckResult){
		"app":         func(r *api.DeploymentResponseCheckResult) { r.AppID = applicationResponseInstanceID },
		"deployment":  func(r *api.DeploymentResponseCheckResult) { r.DeploymentID = applicationResponseInstanceID },
		"instance":    func(r *api.DeploymentResponseCheckResult) { r.InstanceID = "" },
		"scope":       func(r *api.DeploymentResponseCheckResult) { r.Scope = "staging" },
		"project":     func(r *api.DeploymentResponseCheckResult) { r.ProjectID = applicationResponseProjectID },
		"environment": func(r *api.DeploymentResponseCheckResult) { r.EnvironmentID = applicationResponseEnvironmentID },
		"status":      func(r *api.DeploymentResponseCheckResult) { r.HTTPStatus = 0 },
		"body": func(r *api.DeploymentResponseCheckResult) {
			r.Body = strings.Repeat("x", api.DeploymentResponseCheckMaxBodyBytes+1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newApplicationResponseFixture(t)
			change(&f.result)
			backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			defer backend.Close()
			cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true}, "2026-07-28")
			out := callTool(t, ctx, cs, "gregale_verify_application_response", applicationResponseArguments(), true)
			if toolCode(t, out) != "invalid_response" || out["body"] != nil || out["verified"] != nil {
				t.Fatal("unbound evidence was exposed")
			}
		})
	}
}

func TestApplicationResponseNamedEnvironmentAndLateAccessChanges(t *testing.T) {
	for _, after := range []string{"", "recreate", "revoke", "recreate_environment"} {
		t.Run(after, func(t *testing.T) {
			f := newApplicationResponseFixture(t)
			f.afterCheck = after
			f.deployment.Scope, f.result.Scope = "staging", "staging"
			f.result.ProjectID, f.result.EnvironmentID = applicationResponseProjectID, applicationResponseEnvironmentID
			backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			defer backend.Close()
			opts := Options{APIBase: backend.URL, AllowWrites: true, AllowedApps: map[string]string{"alpha": applicationResponseAppID}, Environments: &EnvironmentConsent{Named: map[string]EnvironmentGrant{
				"shop/staging": {Project: "shop", ProjectID: applicationResponseProjectID, Environment: "staging", EnvironmentID: applicationResponseEnvironmentID, Apps: map[string]string{"alpha": applicationResponseAppID}, Write: true},
			}}}
			cs, ctx := connectSelectedTestServer(t, backend, opts, "2026-07-28")
			args := applicationResponseArguments()
			args["project"], args["environment"] = "shop", "staging"
			out := callTool(t, ctx, cs, "gregale_verify_application_response", args, after != "")
			if after == "" {
				if string(out["verified"]) != "true" || string(out["environment_id"]) != `"`+applicationResponseEnvironmentID+`"` {
					t.Fatal("named environment response was not verified")
				}
			} else if out["body"] != nil {
				t.Fatal("late access change exposed a response body")
			}
		})
	}
}

func TestApplicationResponseDoesNotExecuteUnavailableOrUnconsentedTargets(t *testing.T) {
	for _, mode := range []string{"unsupported", "unavailable", "not_live", "wrong_scope", "wrong_app", "wrong_id", "default_not_consented", "app_not_consented", "environment_not_consented", "capability_denied"} {
		t.Run(mode, func(t *testing.T) {
			f := newApplicationResponseFixture(t)
			opts := Options{AllowWrites: true}
			switch mode {
			case "unsupported":
				f.capability.ProtocolVersion++
			case "unavailable":
				f.capability.Available = false
			case "not_live":
				f.deployment.Status = "building"
			case "wrong_scope":
				f.deployment.Scope = "staging"
			case "wrong_app":
				f.deployment.AppID = applicationResponseInstanceID
			case "wrong_id":
				f.deployment.ID = applicationResponseInstanceID
			case "default_not_consented":
				opts.Environments = &EnvironmentConsent{}
			case "app_not_consented":
				opts.AllowedApps = map[string]string{"beta": applicationResponseAppID}
			case "environment_not_consented":
				opts.Environments = &EnvironmentConsent{DefaultWrite: true}
			case "capability_denied":
				f.capabilityError = http.StatusForbidden
			}
			backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			defer backend.Close()
			opts.APIBase = backend.URL
			cs, ctx := connectSelectedTestServer(t, backend, opts, "2026-07-28")
			args := applicationResponseArguments()
			if mode == "environment_not_consented" {
				args["project"], args["environment"] = "shop", "staging"
			}
			out := callTool(t, ctx, cs, "gregale_verify_application_response", args, true)
			if strings.HasSuffix(mode, "not_consented") || mode == "capability_denied" {
				var recovery Recovery
				if json.Unmarshal(out["recovery"], &recovery) != nil || recovery.Action != "request_access" {
					t.Fatal("missing consent did not request access")
				}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.checks != 0 {
				t.Fatal("unavailable or unconsented target executed")
			}
		})
	}
}

func TestApplicationResponseTimeoutCancelsNativeCheckWithoutRetry(t *testing.T) {
	f := newApplicationResponseFixture(t)
	cancelled := make(chan struct{})
	f.onCheck = func(ctx context.Context) { <-ctx.Done(); close(cancelled) }
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	defer backend.Close()
	cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true, Timeout: 100 * time.Millisecond}, "2026-07-28")
	out := callTool(t, ctx, cs, "gregale_verify_application_response", applicationResponseArguments(), true)
	var recovery Recovery
	if json.Unmarshal(out["recovery"], &recovery) != nil || recovery.Action != "inspect" || recovery.Tool != "gregale_get_deployment" || string(out["outcome"]) != `"outcome_unknown"` {
		t.Fatal("uncertain execution did not direct inspection of the selected deployment")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("native request was not cancelled")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checks != 1 {
		t.Fatal("active check was retried")
	}
}

func TestApplicationResponseDefaultPathAndUUIDWireForms(t *testing.T) {
	f := newApplicationResponseFixture(t)
	f.path = "/"
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	defer backend.Close()
	cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true}, "2026-07-28")
	args := map[string]any{"app": "alpha", "deployment_id": strings.ReplaceAll(applicationResponseDeploymentID, "-", "")}
	out := callTool(t, ctx, cs, "gregale_verify_application_response", args, false)
	if string(out["path"]) != `"/"` || string(out["deployment_id"]) != `"`+applicationResponseDeploymentID+`"` || string(out["verified"]) != "true" {
		t.Fatal("default path or canonical deployment identity was lost")
	}
}

func TestApplicationResponseBoundsBodyAfterRedaction(t *testing.T) {
	f := newApplicationResponseFixture(t)
	f.result.Body = strings.Repeat("fp_test_a ", api.DeploymentResponseCheckMaxBodyBytes/10)
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	defer backend.Close()
	cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true}, "2026-07-28")
	out := callTool(t, ctx, cs, "gregale_verify_application_response", applicationResponseArguments(), false)
	var body string
	if json.Unmarshal(out["body"], &body) != nil || len(body) > api.DeploymentResponseCheckMaxBodyBytes || strings.Contains(body, "fp_test_a") || string(out["body_truncated"]) != "true" {
		t.Fatal("redaction expansion escaped the body bound or hid truncation")
	}
}

func TestApplicationResponseNativeFailureDoesNotReplay(t *testing.T) {
	f := newApplicationResponseFixture(t)
	f.checkError = http.StatusServiceUnavailable
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	defer backend.Close()
	cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true}, "2026-07-28")
	out := callTool(t, ctx, cs, "gregale_verify_application_response", applicationResponseArguments(), true)
	if toolCode(t, out) != "capacity_unavailable" || string(out["outcome"]) != `"outcome_unknown"` {
		t.Fatal("native failure did not preserve uncertainty")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checks != 1 {
		t.Fatal("failed execution was replayed")
	}
}

func TestApplicationResponseLostConnectionDoesNotReplay(t *testing.T) {
	f := newApplicationResponseFixture(t)
	f.dropConnection = true
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	defer backend.Close()
	cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true}, "2026-07-28")
	out := callTool(t, ctx, cs, "gregale_verify_application_response", applicationResponseArguments(), true)
	if string(out["outcome"]) != `"outcome_unknown"` {
		t.Fatal("lost response did not preserve execution uncertainty")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checks != 1 {
		t.Fatalf("lost response replayed the native check: %d attempts", f.checks)
	}
}

func TestApplicationResponseHostedAppAgentNeedsNoCLIOrAccountInventory(t *testing.T) {
	const token = "fp_test_response_agent"
	scopes := []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}
	f := agentKeyFixture(t, map[string][]string{token: scopes})
	response := newApplicationResponseFixture(t)
	response.apiToken = token
	response.result.Body = "hosted application response"
	f.apiHook = func(w http.ResponseWriter, r *http.Request, account string) bool {
		if r.URL.Path == "/v1/account" {
			_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: account, Plan: "free", Status: "active", KeyScopes: scopes, KeyApp: &api.AppAgentBinding{ID: applicationResponseAppID, Slug: "alpha"}})
		} else {
			response.serveHTTP(w, r)
		}
		return true
	}
	cs, ctx := f.connect(tokenResponse{AccessToken: token}, "2026-07-28")
	out := callTool(t, ctx, cs, "gregale_verify_application_response", applicationResponseArguments(), false)
	if string(out["verified"]) != "true" || string(out["public_access_verified"]) != "false" || string(out["body"]) != `"hosted application response"` {
		t.Fatal("hosted app-bound agent did not receive application evidence")
	}
}
