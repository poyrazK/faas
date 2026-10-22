package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPrimaryDomainOrFallbackUsesCanonicalHost(t *testing.T) {
	tests := []struct {
		name string
		app  api.AppResponse
		want string
	}{
		{
			name: "verified custom domain",
			app: api.AppResponse{
				Slug: "demo", URL: "https://demo.gregale.dev",
				CanonicalURL: "https://api.example.com", DefaultDomain: "api.example.com",
			},
			want: "api.example.com",
		},
		{
			name: "canonical URL without default domain",
			app: api.AppResponse{
				Slug: "demo", URL: "https://demo.gregale.dev",
				CanonicalURL: "https://api.example.com",
			},
			want: "api.example.com",
		},
		{
			name: "platform fallback",
			app:  api.AppResponse{Slug: "demo", URL: "https://demo.gregale.dev"},
			want: "demo.gregale.dev",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := main.PrimaryDomainOrFallback(tt.app); got != tt.want {
				t.Fatalf("primaryDomainOrFallback() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCorsAllowCredentialedDefaultsUseExplicitHeaders(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"id":"rule-1","kind":"cors","match_host":"demo.gregale.dev","match_path":"/*"}`, http.StatusCreated)
	if code := main.CmdCorsAllow([]string{"demo", "https://app.example", "--host", "demo.gregale.dev", "--credentials"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var body struct {
		MatchMethods []string `json:"match_methods"`
		Action       struct {
			AllowHeaders []string `json:"allow_headers"`
			AllowMethods []string `json:"allow_methods"`
		} `json:"action"`
	}
	if err := json.Unmarshal((*f.SawBody()), &body); err != nil {
		t.Fatalf("decode request: %v; body=%s", err, (*f.SawBody()))
	}
	if slices.Contains(body.Action.AllowHeaders, "*") {
		t.Fatalf("credentialed defaults contain wildcard: %v", body.Action.AllowHeaders)
	}
	for _, required := range []string{"Authorization", "Content-Type"} {
		if !slices.Contains(body.Action.AllowHeaders, required) {
			t.Fatalf("credentialed defaults %v missing %s", body.Action.AllowHeaders, required)
		}
	}
	if len(body.MatchMethods) != len((*main.CorsDefaultMethods)) || len(body.Action.AllowMethods) != len((*main.CorsDefaultMethods)) {
		t.Fatalf("default match/allow methods = %v/%v, want %v", body.MatchMethods, body.Action.AllowMethods, (*main.CorsDefaultMethods))
	}
	for _, method := range *main.CorsDefaultMethods {
		if !slices.Contains(body.MatchMethods, method) || !slices.Contains(body.Action.AllowMethods, method) {
			t.Fatalf("default match/allow methods = %v/%v, missing %s", body.MatchMethods, body.Action.AllowMethods, method)
		}
	}
}

func TestCorsAllowUsesGatewayMatchAllPath(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"id":"rule-1","kind":"cors","match_host":"demo.gregale.dev","match_path":"/*"}`, http.StatusCreated)
	if code := main.CmdCorsAllow([]string{"demo", "https://app.example", "--host", "demo.gregale.dev"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var body struct {
		MatchPath string `json:"match_path"`
	}
	if err := json.Unmarshal((*f.SawBody()), &body); err != nil {
		t.Fatal(err)
	}
	if body.MatchPath != "/*" {
		t.Fatalf("match_path = %q, want gateway match-all path", body.MatchPath)
	}
}

func TestCorsAllowCredentialedCustomHeaders(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"id":"rule-1","kind":"cors","match_host":"demo.gregale.dev","match_path":"/*"}`, http.StatusCreated)
	args := []string{"demo", "https://app.example", "--host", "demo.gregale.dev", "--credentials", "--allow-header", "content-type", "--allow-header", "x-audit"}
	if code := main.CmdCorsAllow(args); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var body map[string]any
	if err := json.Unmarshal((*f.SawBody()), &body); err != nil {
		t.Fatal(err)
	}
	action := body["action"].(map[string]any)
	headers := action["allow_headers"].([]any)
	if len(headers) != 2 || headers[0] != "Content-Type" || headers[1] != "X-Audit" {
		t.Fatalf("allow_headers = %v", headers)
	}
}

func TestCorsAllowExplicitMethodsReplaceDefaults(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"id":"rule-1","kind":"cors","match_host":"demo.gregale.dev","match_path":"/*"}`, http.StatusCreated)
	if code := main.CmdCorsAllow([]string{"demo", "https://app.example", "--host", "demo.gregale.dev", "--method", "GET"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var body struct {
		MatchMethods []string `json:"match_methods"`
		Action       struct {
			AllowMethods []string `json:"allow_methods"`
		} `json:"action"`
	}
	if err := json.Unmarshal((*f.SawBody()), &body); err != nil {
		t.Fatalf("decode request: %v; body=%s", err, (*f.SawBody()))
	}
	if !slices.Equal(body.MatchMethods, []string{"GET"}) || !slices.Equal(body.Action.AllowMethods, []string{"GET"}) {
		t.Fatalf("match/allow methods = %v/%v, want GET only", body.MatchMethods, body.Action.AllowMethods)
	}
}

func TestCorsAllowRejectsCredentialedWildcardHeaders(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdCorsAllow([]string{"demo", "https://app.example", "--host", "demo.gregale.dev", "--credentials", "--allow-header", "*"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

// TestCorsAllowIsIdempotent pins H10-119: on production-us two identical
// `cors allow` calls left two identical rules. An existing match is reported
// and nothing is created; a different origin still creates a rule.
func TestCorsAllowIsIdempotent(t *testing.T) {
	main.ResetJSONOut(t)
	existing := api.EdgeRuleCORSAction{AllowOrigins: []string{"https://app.example"}, AllowMethods: []string{"GET", "POST"}, AllowHeaders: []string{"*"}, MaxAgeSeconds: 600}
	raw, err := json.Marshal(existing)
	if err != nil {
		t.Fatal(err)
	}
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/demo/edge-rules":
			main.WriteJSONTest(w, []api.EdgeRuleResponse{{ID: "rule-1", Kind: "cors", Enabled: true, MatchHost: "demo.gregale.dev", MatchPath: "/*", MatchMethods: []string{"POST", "GET"}, Action: raw}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/demo/edge-rules":
			posts++
			w.WriteHeader(http.StatusCreated)
			main.WriteJSONTest(w, api.EdgeRuleResponse{ID: "rule-2", Kind: "cors"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	_, restore := main.CaptureStdout(t)
	defer restore()
	if code := main.CmdCorsAllow([]string{"demo", "https://app.example", "--host", "demo.gregale.dev", "--method", "GET", "--method", "POST"}); code != 0 || posts != 0 {
		t.Fatalf("identical allow: exit=%d posts=%d, want 0 and 0", code, posts)
	}
	if code := main.CmdCorsAllow([]string{"demo", "https://other.example", "--host", "demo.gregale.dev", "--method", "GET", "--method", "POST"}); code != 0 || posts != 1 {
		t.Fatalf("different origin: exit=%d posts=%d, want 0 and 1", code, posts)
	}
}
