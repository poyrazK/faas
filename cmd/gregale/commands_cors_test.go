package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

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
			if got := primaryDomainOrFallback(tt.app); got != tt.want {
				t.Fatalf("primaryDomainOrFallback() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCorsAllowCredentialedDefaultsUseExplicitHeaders(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"rule-1","kind":"cors","match_host":"demo.gregale.dev","match_path":"/*"}`, http.StatusCreated)
	if code := cmdCorsAllow([]string{"demo", "https://app.example", "--host", "demo.gregale.dev", "--credentials"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var body struct {
		MatchMethods []string `json:"match_methods"`
		Action       struct {
			AllowHeaders []string `json:"allow_headers"`
			AllowMethods []string `json:"allow_methods"`
		} `json:"action"`
	}
	if err := json.Unmarshal(f.sawBody, &body); err != nil {
		t.Fatalf("decode request: %v; body=%s", err, f.sawBody)
	}
	if slices.Contains(body.Action.AllowHeaders, "*") {
		t.Fatalf("credentialed defaults contain wildcard: %v", body.Action.AllowHeaders)
	}
	for _, required := range []string{"Authorization", "Content-Type"} {
		if !slices.Contains(body.Action.AllowHeaders, required) {
			t.Fatalf("credentialed defaults %v missing %s", body.Action.AllowHeaders, required)
		}
	}
	if len(body.MatchMethods) != len(corsDefaultMethods) || len(body.Action.AllowMethods) != len(corsDefaultMethods) {
		t.Fatalf("default match/allow methods = %v/%v, want %v", body.MatchMethods, body.Action.AllowMethods, corsDefaultMethods)
	}
	for _, method := range corsDefaultMethods {
		if !slices.Contains(body.MatchMethods, method) || !slices.Contains(body.Action.AllowMethods, method) {
			t.Fatalf("default match/allow methods = %v/%v, missing %s", body.MatchMethods, body.Action.AllowMethods, method)
		}
	}
}

func TestCorsAllowUsesGatewayMatchAllPath(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"rule-1","kind":"cors","match_host":"demo.gregale.dev","match_path":"/*"}`, http.StatusCreated)
	if code := cmdCorsAllow([]string{"demo", "https://app.example", "--host", "demo.gregale.dev"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var body struct {
		MatchPath string `json:"match_path"`
	}
	if err := json.Unmarshal(f.sawBody, &body); err != nil {
		t.Fatal(err)
	}
	if body.MatchPath != "/*" {
		t.Fatalf("match_path = %q, want gateway match-all path", body.MatchPath)
	}
}

func TestCorsAllowCredentialedCustomHeaders(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"rule-1","kind":"cors","match_host":"demo.gregale.dev","match_path":"/*"}`, http.StatusCreated)
	args := []string{"demo", "https://app.example", "--host", "demo.gregale.dev", "--credentials", "--allow-header", "content-type", "--allow-header", "x-audit"}
	if code := cmdCorsAllow(args); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var body map[string]any
	if err := json.Unmarshal(f.sawBody, &body); err != nil {
		t.Fatal(err)
	}
	action := body["action"].(map[string]any)
	headers := action["allow_headers"].([]any)
	if len(headers) != 2 || headers[0] != "Content-Type" || headers[1] != "X-Audit" {
		t.Fatalf("allow_headers = %v", headers)
	}
}

func TestCorsAllowExplicitMethodsReplaceDefaults(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"rule-1","kind":"cors","match_host":"demo.gregale.dev","match_path":"/*"}`, http.StatusCreated)
	if code := cmdCorsAllow([]string{"demo", "https://app.example", "--host", "demo.gregale.dev", "--method", "GET"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var body struct {
		MatchMethods []string `json:"match_methods"`
		Action       struct {
			AllowMethods []string `json:"allow_methods"`
		} `json:"action"`
	}
	if err := json.Unmarshal(f.sawBody, &body); err != nil {
		t.Fatalf("decode request: %v; body=%s", err, f.sawBody)
	}
	if !slices.Equal(body.MatchMethods, []string{"GET"}) || !slices.Equal(body.Action.AllowMethods, []string{"GET"}) {
		t.Fatalf("match/allow methods = %v/%v, want GET only", body.MatchMethods, body.Action.AllowMethods)
	}
}

func TestCorsAllowRejectsCredentialedWildcardHeaders(t *testing.T) {
	resetJSONOut(t)
	if code := cmdCorsAllow([]string{"demo", "https://app.example", "--host", "demo.gregale.dev", "--credentials", "--allow-header", "*"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}
