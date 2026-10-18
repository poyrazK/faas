package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func serviceExplainApps() (api.AppResponse, api.AppResponse) {
	return api.AppResponse{ID: "caller-id", Slug: "frontend", ServiceBindingFingerprint: strings.Repeat("a", 64)}, api.AppResponse{ID: "target-id", Slug: "billing", ServiceBindingFingerprint: strings.Repeat("b", 64)}
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
			report := newServiceExplanation("frontend", "billing", tc.method, tc.path)
			explainServiceAuthorization(&report, caller, target)
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
	method, path, err := serviceExplainRequest("get", "/invoices%2F123?filter=private")
	if err != nil || method != "GET" || path != "/invoices/123" {
		t.Fatalf("%s %s %v", method, path, err)
	}
	for _, path := range []string{"https://example.com/", "//example.com/", "/a/../b", "/a//b", "/%2e%2e/b", "/a%5Cb", "/a#fragment"} {
		if _, _, err := serviceExplainRequest("GET", path); err == nil {
			t.Errorf("accepted %s", path)
		}
	}
	if _, _, err := serviceExplainRequest("GET\r\nX: bad", "/"); err == nil {
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
			resetJSONOut(t)
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
					writeJSONTest(w, caller)
				case "/v1/apps/billing":
					if tc.status != 200 {
						w.Header().Set("Content-Type", "application/problem+json")
						w.WriteHeader(tc.status)
						_, _ = w.Write([]byte(`{"title":"PRIVATE_ERROR","status":` + strconv.Itoa(tc.status) + `}`))
						return
					}
					writeJSONTest(w, target)
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			var output bytes.Buffer
			old := osStdout
			osStdout = &output
			t.Cleanup(func() { osStdout = old })
			if code := run([]string{"bindings", "service", "explain", "frontend", "billing", "--method", "get", "--path", "/invoices?secret=value", "--json"}); code != tc.want {
				t.Fatalf("code=%d output=%s", code, output.String())
			}
			var report serviceAuthorizationExplanation
			if err := json.Unmarshal(output.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Result != tc.result || requests != 2 || strings.Contains(output.String(), "PRIVATE_ERROR") || strings.Contains(output.String(), "secret=value") {
				t.Fatalf("requests=%d report=%s", requests, output.String())
			}
			previous := requests
			if code := run([]string{"bindings", "service", "explain", "frontend", "billing", "--path", "/../bad", "--json"}); code != 2 || requests != previous {
				t.Fatalf("invalid input code=%d requests=%d", code, requests)
			}
		})
	}
}
