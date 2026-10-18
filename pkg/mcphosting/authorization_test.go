package mcphosting

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthorizationRejectionProbes(t *testing.T) {
	for _, scenario := range []string{"healthy", "missing accepted", "malformed forbidden", "scope unauthorized", "scope accepted", "server error", "redirect", "timeout", "legacy"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Method string `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&request)
				switch request.Method {
				case "initialize", "server/discover", "tools/list", "resources/list", "resources/templates/list", "prompts/list":
				default:
					t.Errorf("unsafe probe: %s", request.Method)
				}
				calls.Add(1)
				if r.Header.Get("Authorization") == "Bearer POSITIVE_TOKEN_SENTINEL" {
					t.Error("positive credential reused for rejection probes")
				}
				status := 401
				if r.Header.Get("Authorization") == "Bearer SCOPE_TOKEN_SENTINEL" {
					status = 403
				}
				if scenario == "missing accepted" && r.Header.Get("Authorization") == "" {
					status = 200
				}
				if scenario == "malformed forbidden" && strings.Contains(r.Header.Get("Authorization"), "gregale-invalid") {
					status = 403
				}
				if scenario == "scope unauthorized" && status == 403 {
					status = 401
				}
				if scenario == "scope accepted" && status == 403 {
					status = 200
				}
				if scenario == "server error" {
					status = 503
				}
				if scenario == "redirect" {
					w.Header().Set("Location", "https://unreviewed.invalid")
					status = 302
				}
				if scenario == "timeout" {
					<-r.Context().Done()
					return
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("PRIVATE_BODY_SENTINEL"))
			}))
			defer server.Close()
			version := ProtocolVersion
			if scenario == "legacy" {
				version = LegacyProtocolVersion
			}
			client, _ := NewClient(server.URL+"/mcp", "POSITIVE_TOKEN_SENTINEL", version)
			timeout := 2 * time.Second
			if scenario == "timeout" {
				timeout = 50 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			checks := client.CheckAuthorization(ctx, AuthorizationExpectations{}, Catalog{}, "SCOPE_TOKEN_SENTINEL")
			passed := true
			for _, check := range checks {
				passed = passed && check.Status == "passed"
				if strings.Contains(check.Detail, "SENTINEL") {
					t.Fatal("private body or credentials leaked")
				}
			}
			if passed != (scenario == "healthy" || scenario == "legacy") {
				t.Fatalf("unexpected authorization: %+v", checks)
			}
			if scenario == "healthy" && (calls.Load() != 18 || len(checks) != 18) {
				t.Fatalf("incomplete probe coverage: %d/%d", calls.Load(), len(checks))
			}
			if scenario == "legacy" && calls.Load() != 15 {
				t.Fatalf("legacy probe coverage: %d", calls.Load())
			}
		})
	}
}

func TestAuthorizationCatalogExclusions(t *testing.T) {
	expectations := AuthorizationExpectations{DeniedTools: []string{"write"}, DeniedResources: []string{"file:///secret"}, DeniedResourceTemplates: []string{"file:///secret/{id}"}, DeniedPrompts: []string{"admin"}}
	catalogs := []Catalog{{}, {Tools: []Tool{{Name: "write"}}}, {Resources: []Resource{{URI: "file:///secret"}}}, {ResourceTemplates: []ResourceTemplate{{URITemplate: "file:///secret/{id}"}}}, {Prompts: []Prompt{{Name: "admin"}}}}
	for i, catalog := range catalogs {
		checks := CheckAuthorizationVisibility(expectations, catalog)
		passed := true
		for _, check := range checks {
			passed = passed && check.Status == "passed"
		}
		if passed != (i == 0) {
			t.Fatalf("exclusion %d not enforced: %+v", i, checks)
		}
	}
	for _, entries := range [][]string{{""}, {"write", "write"}, {" write"}, {"secret\n"}, {"sec\tret"}} {
		if (AuthorizationExpectations{DeniedTools: entries}).Validate() == nil {
			t.Fatalf("invalid exclusion accepted: %q", entries)
		}
	}
}
