package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func serviceExplainApps() (api.AppResponse, api.AppResponse) {
	return api.AppResponse{ID: "caller-id", Slug: "frontend", ServiceBindingFingerprint: strings.Repeat("a", 64)}, api.AppResponse{ID: "target-id", Slug: "billing", ServiceBindingFingerprint: strings.Repeat("b", 64)}
}

func TestServiceExplainDeploymentCLI(t *testing.T) {
	deployment := uuid.NewString()
	for _, tc := range []struct {
		name, result string
		code         int
		mutate       func(*api.ServiceBindingExplanation)
		status       int
	}{
		{"allowed", "allowed", 0, nil, 200},
		{"denied", "denied", 1, nil, 200},
		{"unknown", "indeterminate", 2, nil, 200},
		{"old server", "indeterminate", 2, nil, 404},
		{"wrong deployment", "indeterminate", 2, func(r *api.ServiceBindingExplanation) { r.CallerDeploymentID = uuid.NewString() }, 200},
		{"wrong environment", "indeterminate", 2, func(r *api.ServiceBindingExplanation) { r.Scope = "production" }, 200},
		{"missing route", "indeterminate", 2, func(r *api.ServiceBindingExplanation) { r.TargetDeploymentID = "" }, 200},
		{"missing fingerprint", "indeterminate", 2, func(r *api.ServiceBindingExplanation) { r.TargetFingerprint = "" }, 200},
		{"invalid target", "indeterminate", 2, func(r *api.ServiceBindingExplanation) { r.TargetAppID = "not-an-id" }, 200},
		{"conflicting decision", "indeterminate", 2, func(r *api.ServiceBindingExplanation) { r.Checks[0].Result = "denied" }, 200},
		{"empty response", "indeterminate", 2, func(r *api.ServiceBindingExplanation) { *r = api.ServiceBindingExplanation{} }, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/v1/apps/frontend/bindings/services/billing/explanation" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if q := r.URL.Query(); q.Get("deployment_id") != deployment || q.Get("scope") != "staging" || q.Get("method") != "GET" || q.Get("path") != "/invoices" {
					t.Errorf("query=%v", q)
				}
				if tc.status != 200 {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte("PRIVATE_ERROR"))
					return
				}
				report := api.ServiceBindingExplanation{CallerFingerprint: strings.Repeat("a", 64), TargetFingerprint: strings.Repeat("b", 64), Caller: "frontend", Target: "billing", Method: "GET", Path: "/invoices", Result: "allowed", Scope: "staging", CheckedAt: time.Now().UTC(), CallerDeploymentID: deployment, TargetAppID: uuid.NewString(), TargetDeploymentID: uuid.NewString(), ReleaseID: uuid.NewString(), PolicySource: "environment_release", Transport: api.ServiceBindingTransportHTTPS, Checks: []api.ServiceAuthorizationCheck{{Rule: "routing", Result: "allowed", Reason: "active_reviewed_release"}}, Limitations: "Metadata observation only."}
				if tc.name == "denied" || tc.name == "unknown" {
					report.Result = tc.result
					report.Checks[0].Result = tc.result
				}
				if tc.mutate != nil {
					tc.mutate(&report)
				}
				main.WriteJSONTest(w, report)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			var output bytes.Buffer
			old := (*main.OsStdout)
			(*main.OsStdout) = &output
			t.Cleanup(func() { (*main.OsStdout) = old })
			args := []string{"bindings", "service", "explain", "frontend", "billing", "--deployment", deployment, "--scope", "staging", "--path", "/invoices?secret=PRIVATE_QUERY", "--json"}
			if code := main.Run(args); code != tc.code {
				t.Fatalf("code=%d output=%s", code, &output)
			}
			var got api.ServiceBindingExplanation
			if err := json.Unmarshal(output.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Result != tc.result || calls != 1 || strings.Contains(output.String(), "PRIVATE_") {
				t.Fatalf("calls=%d output=%s", calls, &output)
			}
		})
	}
}

func TestServiceExplainDeploymentRejectsInvalidSelectors(t *testing.T) {
	for _, args := range [][]string{{"--scope", "staging"}, {"--scope", ""}, {"--deployment", ""}, {"--deployment", "bad"}, {"--deployment", uuid.Nil.String()}, {"--deployment", uuid.NewString(), "--scope", "BAD"}} {
		main.ResetJSONOut(t)
		if code := main.Run(append([]string{"bindings", "service", "explain", "frontend", "billing", "--json"}, args...)); code != 2 {
			t.Fatalf("args=%v code=%d", args, code)
		}
	}
}

func TestServiceAuthorizationExplanation(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, result, reason string
		configure                          func(*api.AppResponse, *api.AppResponse)
	}{
		{"default account", "GET", "/invoices", "allowed", "account_policy", nil},
		{"declared missing", "GET", "/invoices", "denied", "target_not_declared", func(c, t *api.AppResponse) { c.ServiceBindingPolicy = api.ServiceBindingPolicyDeclared }},
		{"declared present", "GET", "/invoices", "allowed", "target_declared", func(c, t *api.AppResponse) {
			c.ServiceBindingPolicy = api.ServiceBindingPolicyDeclared
			c.ServiceBindings = []api.AppServiceBinding{{Service: "billing"}}
		}},
		{"empty callers", "GET", "/invoices", "denied", "caller_not_listed", func(c, t *api.AppResponse) { var names []string; t.AllowedServiceCallers = &names }},
		{"caller listed", "GET", "/invoices", "allowed", "caller_listed", func(c, t *api.AppResponse) { names := []string{"frontend"}; t.AllowedServiceCallers = &names }},
		{"caller whitespace matches gateway", "GET", "/invoices", "denied", "caller_not_listed", func(c, t *api.AppResponse) { names := []string{" frontend "}; t.AllowedServiceCallers = &names }},
		{"scope missing", "GET", "/invoices", "denied", "caller_scope_missing", func(c, t *api.AppResponse) { t.AllowedServiceCallScopes = &api.ServiceCallerScopes{} }},
		{"scope exact", "GET", "/invoices", "allowed", "method_path_granted", scopeExplainFixture},
		{"scope descendant", "GET", "/invoices/123", "allowed", "method_path_granted", scopeExplainFixture},
		{"scope boundary", "GET", "/invoices-private", "denied", "method_path_not_granted", scopeExplainFixture},
		{"scope method", "POST", "/invoices", "denied", "method_path_not_granted", scopeExplainFixture},
		{"intersection", "GET", "/invoices", "denied", "caller_not_listed", func(c, t *api.AppResponse) {
			scopeExplainFixture(c, t)
			names := []string{"worker"}
			t.AllowedServiceCallers = &names
		}},
		{"wildcard", "DELETE", "/anything", "allowed", "method_path_granted", func(c, t *api.AppResponse) {
			t.AllowedServiceCallScopes = &api.ServiceCallerScopes{"frontend": {Methods: []string{"*"}, PathPrefixes: []string{"/"}}}
		}},
		{"preview caller", "GET", "/", "indeterminate", "preview_context_unsupported", func(c, t *api.AppResponse) { c.PreviewOfSlug = "parent" }},
		{"preview target", "GET", "/", "indeterminate", "preview_context_unsupported", func(c, t *api.AppResponse) { t.PreviewOfSlug = "parent" }},
		{"old server", "GET", "/", "indeterminate", "policy_contract_unavailable", func(c, t *api.AppResponse) { c.ServiceBindingFingerprint = "" }},
		{"unknown policy", "GET", "/", "indeterminate", "unknown_outbound_policy", func(c, t *api.AppResponse) { c.ServiceBindingPolicy = "future" }},
		{"unknown transport", "GET", "/", "indeterminate", "unknown_transport", func(c, t *api.AppResponse) { c.ServiceBindingTransport = "future" }},
		{"invalid scope", "GET", "/", "indeterminate", "invalid_scope_policy", func(c, t *api.AppResponse) { t.AllowedServiceCallScopes = &api.ServiceCallerScopes{"frontend": {}} }},
		{"deleted", "GET", "/", "denied", "app_deleted", func(c, t *api.AppResponse) { t.Status = "deleted" }},
		{"identity mismatch", "GET", "/", "indeterminate", "app_identity_mismatch", func(c, t *api.AppResponse) { t.Slug = "other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller, target := serviceExplainApps()
			if tc.configure != nil {
				tc.configure(&caller, &target)
			}
			report := main.NewServiceExplanation("frontend", "billing", tc.method, tc.path)
			main.ExplainServiceAuthorization(&report, caller, target)
			if report.Result != tc.result {
				t.Fatalf("report=%+v", report)
			}
			found := false
			for _, check := range report.Checks {
				if check.Reason == tc.reason {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing reason %s: %+v", tc.reason, report)
			}
		})
	}
}

func scopeExplainFixture(c, t *api.AppResponse) {
	t.AllowedServiceCallScopes = &api.ServiceCallerScopes{"frontend": {Methods: []string{"GET"}, PathPrefixes: []string{"/invoices"}}}
}

func TestServiceExplainRequest(t *testing.T) {
	method, path, err := main.ServiceExplainRequest("get", "/invoices%2F123?filter=private")
	if err != nil || method != "GET" || path != "/invoices/123" {
		t.Fatalf("%s %s %v", method, path, err)
	}
	for _, path := range []string{"https://example.com/", "//example.com/", "/a/../b", "/a//b", "/%2e%2e/b", "/a%5Cb", "/a#fragment"} {
		if _, _, err := main.ServiceExplainRequest("GET", path); err == nil {
			t.Errorf("accepted %s", path)
		}
	}
	if _, _, err := main.ServiceExplainRequest("GET\r\nX: bad", "/"); err == nil {
		t.Fatal("accepted malformed method")
	}
}

func TestServiceExplainCLI(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		denied bool
		want   int
		result string
	}{
		{"allowed", 200, false, 0, "allowed"}, {"denied", 200, true, 1, "denied"}, {"missing", 404, false, 2, "indeterminate"}, {"forbidden", 403, false, 2, "indeterminate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			caller, target := serviceExplainApps()
			scopeExplainFixture(&caller, &target)
			if tc.denied {
				names := []string{}
				target.AllowedServiceCallers = &names
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet {
					t.Errorf("unexpected mutation: %s", r.Method)
				}
				switch r.URL.Path {
				case "/v1/apps/frontend":
					main.WriteJSONTest(w, caller)
				case "/v1/apps/billing":
					if tc.status != 200 {
						w.Header().Set("Content-Type", "application/problem+json")
						w.WriteHeader(tc.status)
						_, _ = w.Write([]byte(`{"title":"PRIVATE_ERROR","status":` + strconv.Itoa(tc.status) + `}`))
						return
					}
					main.WriteJSONTest(w, target)
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			var output bytes.Buffer
			old := (*main.OsStdout)
			(*main.OsStdout) = &output
			t.Cleanup(func() { (*main.OsStdout) = old })
			if code := main.Run([]string{"bindings", "service", "explain", "frontend", "billing", "--method", "get", "--path", "/invoices?secret=value", "--json"}); code != tc.want {
				t.Fatalf("code=%d output=%s", code, output.String())
			}
			var report main.ServiceAuthorizationExplanation
			if err := json.Unmarshal(output.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Result != tc.result || requests != 2 || strings.Contains(output.String(), "PRIVATE_ERROR") || strings.Contains(output.String(), "secret=value") {
				t.Fatalf("requests=%d report=%s", requests, output.String())
			}
			previous := requests
			if code := main.Run([]string{"bindings", "service", "explain", "frontend", "billing", "--path", "/../bad", "--json"}); code != 2 || requests != previous {
				t.Fatalf("invalid input code=%d requests=%d", code, requests)
			}
		})
	}
}
