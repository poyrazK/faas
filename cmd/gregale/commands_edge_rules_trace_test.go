package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPreviewEdgeRules(t *testing.T) {
	base := api.EdgeRuleResponse{Enabled: true, Kind: "ip", MatchHost: "*.example.com", MatchPath: "/api/*", MatchMethods: []string{"GET"}, Priority: 10}
	makeRule := func(id string, change func(*api.EdgeRuleResponse)) api.EdgeRuleResponse {
		r := base
		r.ID = id
		if change != nil {
			change(&r)
		}
		return r
	}
	rules := []api.EdgeRuleResponse{
		makeRule("late", func(r *api.EdgeRuleResponse) { r.CreatedAt = time.Unix(20, 0) }),
		makeRule("disabled", func(r *api.EdgeRuleResponse) { r.Enabled = false }),
		makeRule("host", func(r *api.EdgeRuleResponse) { r.MatchHost = "elsewhere.example" }),
		makeRule("method", func(r *api.EdgeRuleResponse) { r.MatchMethods = []string{"POST"} }),
		makeRule("path", func(r *api.EdgeRuleResponse) { r.MatchPath = "/other/*" }),
		makeRule("invalid", func(r *api.EdgeRuleResponse) { r.MatchPath = "[" }),
		makeRule("early", func(r *api.EdgeRuleResponse) { r.Priority = 5; r.CreatedAt = time.Unix(10, 0) }),
		makeRule("other-kind", func(r *api.EdgeRuleResponse) { r.Kind = "headers" }),
	}
	got := previewEdgeRules("demo", "api.example.com", "/api/items", "GET", "", "", rules)
	byID := make(map[string]edgeRuleTraceRow)
	for _, row := range got.Rules {
		byID[row.ID] = row
	}
	for _, id := range []string{"disabled", "host", "method", "path", "invalid"} {
		if byID[id].Status != "skipped" || byID[id].Reason == "" {
			t.Errorf("%s = %#v, want explained skip", id, byID[id])
		}
	}
	if byID["early"].Status != "first_candidate" || byID["late"].Status != "later_candidate" || byID["late"].PrecededBy != "early" {
		t.Errorf("priority order: early=%#v late=%#v", byID["early"], byID["late"])
	}
	if byID["other-kind"].Status != "first_candidate" {
		t.Errorf("per-kind precedence: %#v", byID["other-kind"])
	}
	if !strings.Contains(got.Scope, "not simulated") {
		t.Errorf("missing scope warning: %q", got.Scope)
	}
}

func TestPreviewEdgeRules_EqualPriorityIsAmbiguous(t *testing.T) {
	rules := []api.EdgeRuleResponse{
		{ID: "a", Enabled: true, Kind: "ip", MatchHost: "*", Priority: 10},
		{ID: "b", Enabled: true, Kind: "ip", MatchHost: "*", Priority: 10},
	}
	got := previewEdgeRules("demo", "example.com", "/", "GET", "", "", rules)
	for _, row := range got.Rules {
		if row.Status != "tied_candidate" || !strings.Contains(row.Reason, "no guaranteed order") {
			t.Errorf("tie not qualified: %#v", row)
		}
	}
}

func TestTraceHostAndPathSemantics(t *testing.T) {
	for _, tc := range []struct {
		pattern, host string
		want          bool
	}{
		{"*", "example.com", true},
		{"example.com", "example.com", true},
		{"*.example.com", "api.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "badexample.com", false},
	} {
		if got := traceHostMatches(tc.pattern, tc.host); got != tc.want {
			t.Errorf("traceHostMatches(%q, %q) = %v, want %v", tc.pattern, tc.host, got, tc.want)
		}
	}
	if !traceMethodMatches(nil, "GET") || !traceMethodMatches([]string{"get"}, "GET") || traceMethodMatches([]string{"POST"}, "GET") {
		t.Fatal("method filter differs from gateway")
	}
	rules := []api.EdgeRuleResponse{{ID: "all", Enabled: true, Kind: "route", MatchHost: "*", MatchPath: "*"}}
	if got := previewEdgeRules("demo", "example.com", "/a/b", "GET", "", "", rules).Rules[0].Status; got != "first_candidate" {
		t.Fatalf("match-all path = %q", got)
	}
}

func TestCmdEdgeRulesTrace_JSONAndReadOnly(t *testing.T) {
	resetJSONEnv(t)
	jsonOutput = true
	defer resetJSONEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/demo/edge-rules" {
			t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]api.EdgeRuleResponse{{ID: "match", Enabled: true, Kind: "redirect", MatchHost: "example.com", MatchPath: "/api/*"}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()
	if code := cmdEdgeRules([]string{"trace", "--app", "demo", "--url", "https://EXAMPLE.com/api/items?q=private", "--method", "post"}); code != 0 {
		t.Fatalf("trace exit = %d", code)
	}
	var result edgeRuleTraceResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Host != "example.com" || result.Path != "/api/items" || result.Method != "POST" || len(result.Rules) != 1 || result.Rules[0].Status != "first_candidate" {
		t.Errorf("result = %#v", result)
	}
	if strings.Contains(stdout.String(), "private") {
		t.Fatal("query string leaked into trace output")
	}
}

func TestCmdEdgeRulesTrace_InvalidURLDoesNotCallAPI(t *testing.T) {
	resetJSONEnv(t)
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdEdgeRulesTrace([]string{"--app", "demo", "--url", "file:///tmp/foo"}); code == 0 {
		t.Fatal("expected invalid URL error")
	}
	if called {
		t.Fatal("API called despite invalid URL")
	}
}
