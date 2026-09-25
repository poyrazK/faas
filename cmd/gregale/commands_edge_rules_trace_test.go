package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	if !strings.Contains(got.Scope, "not that the app will return successfully") {
		t.Fatalf("scope does not disclaim app/runtime execution: %q", got.Scope)
	}
	if got.Rules[0].Outcome != "fixed_response" || got.Rules[0].ActionPreview == nil || got.Rules[0].ActionPreview.StatusCode != 200 {
		t.Fatalf("respond preview = %#v", got.Rules[0])
	}
	if got.Simulation.Outcome != "fixed_response" || got.Simulation.Status != "incomplete" {
		t.Fatalf("composed simulation = %#v", got.Simulation)
	}
}

func TestSimulateEdgeRuleRequest_ComposesRewriteHeadersAndResponse(t *testing.T) {
	rules := []api.EdgeRuleResponse{
		{ID: "rewrite", Enabled: true, Kind: "rewrite", MatchHost: "*", MatchPath: "/legacy/*", Priority: 10,
			Action: json.RawMessage(`{"rewrite":{"from":"/legacy","to":"/v1"}}`)},
		{ID: "headers", Enabled: true, Kind: "headers", MatchHost: "*", MatchPath: "/v1/*", MatchHeaders: map[string]string{"x-mode": "on"}, Priority: 10,
			Action: json.RawMessage(`{"headers":{"request_headers":[{"name":"X-Stage","value":"ready","action":"set"}],"response_headers":[{"name":"X-Trace-Preview","value":"yes","action":"add"}]}}`)},
		{ID: "respond", Enabled: true, Kind: "respond", MatchHost: "*", MatchPath: "/v1/*", MatchHeaders: map[string]string{"x-stage": "ready"}, Priority: 10,
			Action: json.RawMessage(`{"respond":{"status_code":202,"body":{"accepted":true}}}`)},
	}
	requestHeaders := http.Header{"X-Mode": []string{"on"}}
	got := simulateEdgeRuleRequest("example.com", "/legacy/items", "GET", "", "", rules, requestHeaders)
	if got.Outcome != "fixed_response" || got.StatusCode != http.StatusAccepted || got.FinalPath != "/v1/items" {
		t.Fatalf("simulation = %#v", got)
	}
	if len(got.Steps) != 3 || got.Steps[0].Phase != "rewrite" || got.Steps[1].Phase != "headers" || got.Steps[2].Phase != "respond" {
		t.Fatalf("phase order = %#v", got.Steps)
	}
	if values := got.RequestHeaders["x-stage"]; len(values) != 1 || values[0] != "ready" {
		t.Fatalf("simulated request headers = %#v", got.RequestHeaders)
	}
	if len(got.ResponseHeaderOps) != 1 || got.ResponseHeaderOps[0].Name != "X-Trace-Preview" {
		t.Fatalf("simulated response operations = %#v", got.ResponseHeaderOps)
	}
	if string(got.Body) != `{"accepted":true}` {
		t.Fatalf("simulated response body = %s", got.Body)
	}
}

func TestSimulateEdgeRuleRequest_StopsAtGatewayShortCircuit(t *testing.T) {
	rules := []api.EdgeRuleResponse{
		{ID: "redirect", Enabled: true, Kind: "redirect", MatchHost: "*", MatchPath: "/legacy/*", Priority: 10,
			Action: json.RawMessage(`{"redirect":{"status_code":307,"to":"/new"}}`)},
		{ID: "rewrite", Enabled: true, Kind: "rewrite", MatchHost: "*", MatchPath: "/legacy/*", Priority: 10,
			Action: json.RawMessage(`{"rewrite":{"from":"/legacy","to":"/v1"}}`)},
		{ID: "headers", Enabled: true, Kind: "headers", MatchHost: "*", MatchPath: "/legacy/*", Priority: 10,
			Action: json.RawMessage(`{"headers":{"request_headers":[{"name":"X-Should-Not-Apply","value":"yes","action":"set"}]}}`)},
	}
	got := simulateEdgeRuleRequest("example.com", "/legacy/items", "GET", "", "", rules, nil)
	if got.Outcome != "redirect" || got.StatusCode != http.StatusTemporaryRedirect || got.Location != "/new" || got.FinalPath != "/legacy/items" {
		t.Fatalf("simulation = %#v", got)
	}
	if len(got.Steps) != 1 || got.Steps[0].RuleID != "redirect" {
		t.Fatalf("later phases ran after redirect: %#v", got.Steps)
	}
}

func TestSimulateEdgeRuleRequest_MaintenanceResponse(t *testing.T) {
	rule := api.EdgeRuleResponse{ID: "maintenance", Enabled: true, Kind: "maintenance", MatchHost: "*", MatchPath: "/", Priority: 10,
		Action: json.RawMessage(`{"maintenance":{"message":"back soon","retry_after_seconds":120}}`)}
	got := simulateEdgeRuleRequest("example.com", "/", "GET", "", "", []api.EdgeRuleResponse{rule}, nil)
	if got.Status != "complete" || got.Outcome != "maintenance" || got.StatusCode != http.StatusServiceUnavailable || got.RetryAfterSeconds != 120 || got.Message != "back soon" {
		t.Fatalf("simulation = %#v", got)
	}
	if len(got.Steps) != 1 || got.Steps[0].Phase != "maintenance" || got.Steps[0].StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("simulation steps = %#v", got.Steps)
	}
}

func TestSimulateEdgeRuleRequest_StopsAtUnknownOrAmbiguousPhase(t *testing.T) {
	t.Run("runtime context barrier", func(t *testing.T) {
		rules := []api.EdgeRuleResponse{
			{ID: "jwt", Enabled: true, Kind: "jwt", MatchHost: "*", MatchPath: "/", Priority: 10, Action: json.RawMessage(`{"jwt":{}}`)},
			{ID: "ip", Enabled: true, Kind: "ip", MatchHost: "*", MatchPath: "/", Priority: 10, Action: json.RawMessage(`{"ip":{"deny":["203.0.113.0/24"]}}`)},
			{ID: "respond", Enabled: true, Kind: "respond", MatchHost: "*", MatchPath: "/", Priority: 10, Action: json.RawMessage(`{"respond":{"status_code":200}}`)},
		}
		got := simulateEdgeRuleRequest("example.com", "/", "GET", "203.0.113.5", "", rules, nil)
		if got.Status != "incomplete" || got.StoppedAt != "jwt" || len(got.Steps) != 1 {
			t.Fatalf("simulation crossed a context barrier: %#v", got)
		}
	})

	t.Run("priority tie", func(t *testing.T) {
		rules := []api.EdgeRuleResponse{
			{ID: "redirect-a", Enabled: true, Kind: "redirect", MatchHost: "*", MatchPath: "/", Priority: 10, Action: json.RawMessage(`{"redirect":{"to":"/a"}}`)},
			{ID: "redirect-b", Enabled: true, Kind: "redirect", MatchHost: "*", MatchPath: "/", Priority: 10, Action: json.RawMessage(`{"redirect":{"to":"/b"}}`)},
		}
		got := simulateEdgeRuleRequest("example.com", "/", "GET", "", "", rules, nil)
		if got.Status != "incomplete" || got.Outcome != "ambiguous" || got.StoppedAt != "redirect" {
			t.Fatalf("simulation hid priority tie: %#v", got)
		}
	})
}

func TestSimulateEdgeRuleRequest_EvaluatesExplicitIPAndGeoContext(t *testing.T) {
	cases := []struct {
		kind, action, clientIP, country string
		wantPhase                       string
	}{
		{kind: "ip", action: `{"ip":{"deny":["203.0.113.0/24"]}}`, clientIP: "203.0.113.5", wantPhase: "ip"},
		{kind: "geo", action: `{"geo":{"allow":["US"]}}`, country: "CA", wantPhase: "geo"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			rule := api.EdgeRuleResponse{ID: "gate", Enabled: true, Kind: tc.kind, MatchHost: "*", MatchPath: "/", Priority: 10, Action: json.RawMessage(tc.action)}
			got := simulateEdgeRuleRequest("example.com", "/", "GET", tc.clientIP, tc.country, []api.EdgeRuleResponse{rule}, nil)
			if got.Outcome != "blocked" || got.StatusCode != http.StatusForbidden || got.StoppedAt != tc.wantPhase {
				t.Fatalf("simulation = %#v", got)
			}
		})
	}
}

func TestSimulateEdgeRuleRequest_RouteNeedsTargetRules(t *testing.T) {
	rule := api.EdgeRuleResponse{ID: "route", Enabled: true, Kind: "route", MatchHost: "*", MatchPath: "/", Priority: 10,
		Action: json.RawMessage(`{"route":{"target_app_slug":"other-app"}}`)}
	got := simulateEdgeRuleRequest("example.com", "/", "GET", "", "", []api.EdgeRuleResponse{rule}, nil)
	if got.Status != "incomplete" || got.Outcome != "route" || got.TargetApp != "other-app" || got.StoppedAt != "route" {
		t.Fatalf("simulation = %#v", got)
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
		if r.Method != http.MethodGet {
			t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1/apps/demo":
			_ = json.NewEncoder(w).Encode(api.AppResponse{})
		case "/v1/apps/demo/edge-rules":
			_ = json.NewEncoder(w).Encode([]api.EdgeRuleResponse{{
				ID: "match", Enabled: true, Kind: "redirect", MatchHost: "example.com", MatchPath: "/api/*",
				Action: json.RawMessage(`{"redirect":{"to":"/elsewhere"}}`),
			}})
		default:
			t.Errorf("unexpected API path %s", r.URL.Path)
			http.NotFound(w, r)
		}
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
	if result.Host != "example.com" || result.Path != "/api/items" || result.Method != "POST" || len(result.Rules) != 1 || result.Rules[0].Status != "first_candidate" || result.Simulation.Outcome != "redirect" || result.Simulation.Location != "/elsewhere" {
		t.Errorf("result = %#v", result)
	}
	if strings.Contains(stdout.String(), "private") {
		t.Fatal("query string leaked into trace output")
	}
}

func TestCmdEdgeRulesTraceSimulatesAppMaintenance(t *testing.T) {
	resetJSONEnv(t)
	jsonOutput = true
	defer resetJSONEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1/apps/demo":
			_ = json.NewEncoder(w).Encode(api.AppResponse{MaintenanceMode: true})
		case "/v1/apps/demo/edge-rules":
			_ = json.NewEncoder(w).Encode([]api.EdgeRuleResponse{{
				ID: "redirect", Enabled: true, Kind: "redirect", MatchHost: "example.com", MatchPath: "*",
				Action: json.RawMessage(`{"redirect":{"to":"/new"}}`),
			}})
		default:
			t.Errorf("unexpected API path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	t.Setenv("FAAS_API_KEY", "")
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()
	if code := cmdEdgeRulesTrace([]string{"--app", "demo", "--url", "https://example.com/"}); code != 0 {
		t.Fatalf("trace exit = %d; output=%s", code, stdout.String())
	}
	var result edgeRuleTraceResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal trace result: %v\n%s", err, stdout.String())
	}
	if result.Simulation.Outcome != "app_maintenance" || result.Simulation.StatusCode != http.StatusServiceUnavailable || result.Simulation.ProblemCode != api.CodeAppMaintenance || result.Simulation.RetryAfterSeconds != api.EdgeRuleMaintenanceRetryAfterSeconds {
		t.Fatalf("app maintenance trace = %#v", result.Simulation)
	}
}

func TestCmdEdgeRulesTraceLoadsDeclaredRouteOpenAPIFallback(t *testing.T) {
	resetJSONEnv(t)
	jsonOutput = true
	defer resetJSONEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1/apps/demo":
			_ = json.NewEncoder(w).Encode(api.AppResponse{OnlyAllowDeclaredRoutes: true})
		case "/v1/apps/demo/openapi":
			if got := r.URL.Query().Get("source"); got != "manual_import" {
				t.Errorf("OpenAPI source = %q, want manual_import", got)
			}
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write([]byte("openapi: 3.1.0\ninfo:\n  title: demo\n  version: v1\npaths:\n  /widgets/{id}:\n    get:\n      responses: {}\n"))
		case "/v1/apps/demo/edge-rules":
			_ = json.NewEncoder(w).Encode([]api.EdgeRuleResponse{})
		default:
			t.Errorf("unexpected API path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	t.Setenv("FAAS_API_KEY", "")
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()
	if code := cmdEdgeRulesTrace([]string{"--app", "demo", "--url", "https://example.com/widgets/42", "--method", "HEAD"}); code != 0 {
		t.Fatalf("trace exit = %d; output=%s", code, stdout.String())
	}
	var result edgeRuleTraceResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal trace result: %v\n%s", err, stdout.String())
	}
	if result.Simulation.Status != "complete" || result.Simulation.Outcome != "continue" || len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].Phase != "declared_routes" || result.Simulation.Steps[0].Outcome != "allowed" {
		t.Fatalf("declared-route OpenAPI trace = %#v", result.Simulation)
	}
}

func TestCmdEdgeRulesTraceReportsMissingDeclaredRouteDocument(t *testing.T) {
	resetJSONEnv(t)
	jsonOutput = true
	defer resetJSONEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/demo":
			_ = json.NewEncoder(w).Encode(api.AppResponse{OnlyAllowDeclaredRoutes: true})
		case "/v1/apps/demo/openapi":
			http.NotFound(w, r)
		case "/v1/apps/demo/edge-rules":
			_ = json.NewEncoder(w).Encode([]api.EdgeRuleResponse{})
		default:
			t.Errorf("unexpected API path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	t.Setenv("FAAS_API_KEY", "")
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()
	if code := cmdEdgeRulesTrace([]string{"--app", "demo", "--url", "https://example.com/widgets/42"}); code != 0 {
		t.Fatalf("trace exit = %d; output=%s", code, stdout.String())
	}
	var result edgeRuleTraceResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal trace result: %v\n%s", err, stdout.String())
	}
	if result.Simulation.Status != "complete" || result.Simulation.Outcome != "declared_route_policy_unavailable" || result.Simulation.StatusCode != http.StatusServiceUnavailable || result.Simulation.ProblemCode != api.CodeDeclaredRoutePolicyUnavailable {
		t.Fatalf("missing declared-route document trace = %#v", result.Simulation)
	}
}

func TestCmdEdgeRulesTraceResolvesCORSRulePreset(t *testing.T) {
	resetJSONEnv(t)
	jsonOutput = true
	defer resetJSONEnv(t)
	presetID := "preset-1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1/apps/demo":
			_ = json.NewEncoder(w).Encode(api.AppResponse{})
		case "/v1/apps/demo/edge-rules":
			_ = json.NewEncoder(w).Encode([]api.EdgeRuleResponse{{
				ID: "cors", AccountID: "acct-1", Enabled: true, Kind: "cors", MatchHost: "example.com", MatchPath: "/", MatchMethods: []string{"GET"},
				Action: json.RawMessage(`{"cors":{"cors_preset_id":"preset-1"}}`),
			}})
		case "/v1/cors-presets":
			_ = json.NewEncoder(w).Encode(api.CorsPresetListResponse{Presets: []api.CorsPresetResponse{{
				ID: presetID, AccountID: "acct-1", AllowOrigins: []string{"https://app.example.com"}, AllowMethods: []string{"GET"},
			}}})
		default:
			t.Errorf("unexpected API path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	t.Setenv("FAAS_API_KEY", "")
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()
	if code := cmdEdgeRules([]string{"trace", "--app", "demo", "--url", "https://example.com/", "--header", "Origin:https://app.example.com"}); code != 0 {
		t.Fatalf("trace exit = %d; output=%s", code, stdout.String())
	}
	var result edgeRuleTraceResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal trace result: %v\n%s", err, stdout.String())
	}
	if result.Simulation.Status != "complete" || result.Simulation.Outcome != "continue" || len(result.Simulation.ResponseHeaderOps) != 2 || result.Simulation.ResponseHeaderOps[0].Value != "https://app.example.com" {
		t.Fatalf("preset-backed trace result = %#v", result.Simulation)
	}
}

func TestCmdEdgeRulesTraceLoadsAppCORSDefaults(t *testing.T) {
	resetJSONEnv(t)
	jsonOutput = true
	defer resetJSONEnv(t)
	enabled := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1/apps/demo":
			_ = json.NewEncoder(w).Encode(api.AppResponse{
				CORSDefaultEnabled: &enabled, CORSDefaultOrigins: []string{"https://*.example.com"},
			})
		case "/v1/apps/demo/edge-rules":
			_ = json.NewEncoder(w).Encode([]api.EdgeRuleResponse{})
		default:
			t.Errorf("unexpected API path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	t.Setenv("FAAS_API_KEY", "")
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()
	if code := cmdEdgeRulesTrace([]string{"--app", "demo", "--url", "https://example.com/", "--header", "Origin:https://app.example.com"}); code != 0 {
		t.Fatalf("trace exit = %d; output=%s", code, stdout.String())
	}
	var result edgeRuleTraceResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal trace result: %v\n%s", err, stdout.String())
	}
	if result.Simulation.Status != "complete" || result.Simulation.Outcome != "continue" || len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].Outcome != "cors_default_applied" || len(result.Simulation.ResponseHeaderOps) != 4 {
		t.Fatalf("app-default CORS trace = %#v", result.Simulation)
	}
}

func TestCmdEdgeRulesTrace_BodyFileValidatesAndOmitsContents(t *testing.T) {
	resetJSONEnv(t)
	jsonOutput = true
	defer resetJSONEnv(t)
	body := `{"count":"private-value"}`
	bodyPath := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(bodyPath, []byte(body), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
		}
		switch r.URL.Path {
		case "/v1/apps/demo":
			_ = json.NewEncoder(w).Encode(api.AppResponse{EffectiveLimits: api.AppEffectiveLimits{RequestBodyMaxBytes: 1 << 20}})
		case "/v1/apps/demo/edge-rules":
			_ = json.NewEncoder(w).Encode([]api.EdgeRuleResponse{{
				ID: "validate", Enabled: true, Kind: "validate", MatchHost: "example.com", MatchPath: "/submit", ValidateMode: api.ValidateModeBlock,
				Action: json.RawMessage(`{"validate":{"schema":{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"]},"content_types":["application/json"]}}`),
			}})
		default:
			t.Errorf("unexpected API path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	t.Setenv("FAAS_API_KEY", "")
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()
	if code := cmdEdgeRulesTrace([]string{"--app", "demo", "--url", "https://example.com/submit", "--method", "POST", "--header", "Content-Type: application/json", "--body-file", bodyPath}); code != 0 {
		t.Fatalf("trace exit = %d; output=%s", code, stdout.String())
	}
	var result edgeRuleTraceResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal trace result: %v\n%s", err, stdout.String())
	}
	if !result.BodyProvided || result.BodyBytes != len(body) || result.Simulation.StatusCode != http.StatusUnprocessableEntity || result.Simulation.Outcome != "validation_failed" {
		t.Fatalf("trace result = %#v", result)
	}
	if strings.Contains(stdout.String(), "private-value") || strings.Contains(stdout.String(), body) {
		t.Fatalf("trace output leaked request body: %s", stdout.String())
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
