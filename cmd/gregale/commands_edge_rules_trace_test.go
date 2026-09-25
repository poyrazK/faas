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

func TestPreviewEdgeRules_DeterministicActionPreviews(t *testing.T) {
	cases := []struct {
		name          string
		kind          string
		action        string
		requestPath   string
		wantOutcome   string
		wantType      string
		wantPath      string
		wantStatus    int
		wantRetry     int
		wantTargetApp string
	}{
		{
			name: "route target", kind: "route",
			action:      `{"route":{"target_app_slug":"legacy-api"}}`,
			wantOutcome: "route", wantType: "route", wantTargetApp: "legacy-api",
		},
		{
			name: "rewrite path", kind: "rewrite", requestPath: "/api/items",
			action:      `{"rewrite":{"from":"/api","to":"/v1"}}`,
			wantOutcome: "rewrite", wantType: "rewrite", wantPath: "/v1/items",
		},
		{
			name: "rewrite prefix mismatch", kind: "rewrite", requestPath: "/api/items",
			action:      `{"rewrite":{"from":"/v2","to":"/v1"}}`,
			wantOutcome: "not_applied", wantType: "rewrite", wantPath: "/api/items",
		},
		{
			name: "redirect", kind: "redirect",
			action:      `{"redirect":{"status_code":307,"to":"/new-path","headers":{"X-Redirect":"yes"}}}`,
			wantOutcome: "redirect", wantType: "redirect", wantStatus: 307,
		},
		{
			name: "headers", kind: "headers",
			action:      `{"headers":{"request_headers":[{"name":"X-Request","action":"set","value":"yes"}],"response_headers":[{"name":"X-Response","action":"add","value":"yes"}]}}`,
			wantOutcome: "headers", wantType: "headers",
		},
		{
			name: "maintenance default", kind: "maintenance",
			action:      `{"maintenance":{"message":"back soon"}}`,
			wantOutcome: "maintenance", wantType: "maintenance", wantStatus: http.StatusServiceUnavailable, wantRetry: api.EdgeRuleMaintenanceRetryAfterSeconds,
		},
		{
			name: "fixed response", kind: "respond",
			action:      `{"respond":{"status_code":201,"body":{"ok":true}}}`,
			wantOutcome: "fixed_response", wantType: "respond", wantStatus: 201,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requestPath := tc.requestPath
			if requestPath == "" {
				requestPath = "/"
			}
			rule := api.EdgeRuleResponse{
				ID: "rule", Enabled: true, Kind: tc.kind, MatchHost: "*", MatchPath: "*",
				Action: json.RawMessage(tc.action),
			}
			got := previewEdgeRules("demo", "example.com", requestPath, "GET", "", "", []api.EdgeRuleResponse{rule})
			if len(got.Rules) != 1 {
				t.Fatalf("got %d rows, want 1", len(got.Rules))
			}
			row := got.Rules[0]
			if row.Outcome != tc.wantOutcome || row.ActionPreview == nil || row.ActionPreview.Type != tc.wantType {
				t.Fatalf("row = %#v, want outcome %q and preview type %q", row, tc.wantOutcome, tc.wantType)
			}
			if tc.wantPath != "" && row.ActionPreview.Path != tc.wantPath {
				t.Errorf("preview path = %q, want %q", row.ActionPreview.Path, tc.wantPath)
			}
			if tc.wantStatus != 0 && row.ActionPreview.StatusCode != tc.wantStatus {
				t.Errorf("preview status = %d, want %d", row.ActionPreview.StatusCode, tc.wantStatus)
			}
			if tc.wantRetry != 0 && row.ActionPreview.RetryAfterSeconds != tc.wantRetry {
				t.Errorf("preview retry-after = %d, want %d", row.ActionPreview.RetryAfterSeconds, tc.wantRetry)
			}
			if tc.wantTargetApp != "" && row.ActionPreview.TargetApp != tc.wantTargetApp {
				t.Errorf("preview target app = %q, want %q", row.ActionPreview.TargetApp, tc.wantTargetApp)
			}
		})
	}
}

func TestPreviewEdgeRules_DeterministicActionPreviewIsExplicitlyPerRule(t *testing.T) {
	rule := api.EdgeRuleResponse{
		ID: "respond", Enabled: true, Kind: "respond", MatchHost: "*", MatchPath: "*",
		Action: json.RawMessage(`{"respond":{"status_code":200,"body":{"ok":true}}}`),
	}
	got := previewEdgeRules("demo", "example.com", "/", "GET", "", "", []api.EdgeRuleResponse{rule})
	if !strings.Contains(got.Scope, "not combined into a final gateway response") {
		t.Fatalf("scope does not disclaim combined execution: %q", got.Scope)
	}
	if got.Rules[0].Outcome != "fixed_response" || got.Rules[0].ActionPreview == nil || got.Rules[0].ActionPreview.StatusCode != 200 {
		t.Fatalf("respond preview = %#v", got.Rules[0])
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
