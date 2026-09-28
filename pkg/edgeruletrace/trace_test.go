package edgeruletrace_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

func TestParseScenarioConfig(t *testing.T) {
	config := `{"version":1,"app":"demo","request":{"url":"https://EXAMPLE.com/submit?ignored=yes","method":"post","headers":["Content-Type: application/json","X-Region: east","X-Region: west","Authorization: Bearer config-secret-123"],"client_ip":"203.0.113.7","country":"us","body":"{\"count\":3}"}}`
	input, err := edgeruletrace.ParseScenarioConfig([]byte(config))
	if err != nil {
		t.Fatalf("ParseScenarioConfig: %v", err)
	}
	if input.App != "demo" || input.Host != "example.com" || input.Path != "/submit" || input.Method != http.MethodPost {
		t.Fatalf("request identity = %#v", input)
	}
	if input.ClientIP != "203.0.113.7" || input.Country != "US" || string(input.Body) != `{"count":3}` || !input.BodyProvided {
		t.Fatalf("request context = %#v", input)
	}
	if got := input.Headers.Values("X-Region"); len(got) != 2 || got[0] != "east" || got[1] != "west" {
		t.Fatalf("repeated headers = %#v", got)
	}
	if got := input.Headers.Get("Authorization"); got != "Bearer config-secret-123" {
		t.Fatalf("scenario input was redacted before evaluation: %q", got)
	}
}

func TestParseScenarioConfigSelectsProjectEnvironment(t *testing.T) {
	input, err := edgeruletrace.ParseScenarioConfig([]byte(`{"version":1,"project":"shop","environment":"staging","app":"api","request":{"url":"https://staging-api.example.com/health"}}`))
	if err != nil {
		t.Fatalf("ParseScenarioConfig: %v", err)
	}
	if input.Project != "shop" || input.Environment != "staging" || input.App != "api" {
		t.Fatalf("environment selection = %#v", input)
	}
}

func TestParseScenarioConfigSupportsBase64AndExplicitEmptyBody(t *testing.T) {
	for _, config := range []string{
		`{"version":1,"app":"demo","request":{"url":"https://example.com","body":""}}`,
		`{"version":1,"app":"demo","request":{"url":"https://example.com","body_base64":"AAEC/w=="}}`,
	} {
		input, err := edgeruletrace.ParseScenarioConfig([]byte(config))
		if err != nil {
			t.Fatalf("ParseScenarioConfig(%s): %v", config, err)
		}
		if !input.BodyProvided {
			t.Errorf("body was not marked as supplied for config %s", config)
		}
	}
	input, err := edgeruletrace.ParseScenarioConfig([]byte(`{"version":1,"app":"demo","request":{"url":"https://example.com","body_base64":"AAEC/w=="}}`))
	if err != nil {
		t.Fatalf("ParseScenarioConfig base64: %v", err)
	}
	if string(input.Body) != string([]byte{0, 1, 2, 255}) {
		t.Fatalf("decoded body = %v", input.Body)
	}
}

func TestParseScenarioConfigRejectsInvalidConfigurations(t *testing.T) {
	tooLargeBody, err := json.Marshal(edgeruletrace.ScenarioConfig{
		Version: edgeruletrace.ScenarioConfigVersion, App: "demo",
		Request: edgeruletrace.ScenarioRequestConfig{URL: "https://example.com", Body: stringPointer(strings.Repeat("x", edgeruletrace.MaxTraceBodyBytes+1))},
	})
	if err != nil {
		t.Fatalf("Marshal oversized scenario: %v", err)
	}
	cases := []struct {
		name   string
		config string
		want   string
	}{
		{"unsupported version", `{"version":2,"app":"demo","request":{"url":"https://example.com"}}`, "unsupported trace scenario config version"},
		{"unknown field", `{"version":1,"app":"demo","request":{"url":"https://example.com","region":"west"}}`, "unknown field"},
		{"trailing JSON", `{"version":1,"app":"demo","request":{"url":"https://example.com"}} {}`, "one JSON object"},
		{"credentials in URL", `{"version":1,"app":"demo","request":{"url":"https://user:password@example.com"}}`, "without credentials"},
		{"both body encodings", `{"version":1,"app":"demo","request":{"url":"https://example.com","body":"x","body_base64":"eA=="}}`, "only one of body or body_base64"},
		{"invalid base64", `{"version":1,"app":"demo","request":{"url":"https://example.com","body_base64":"%%%"}}`, "valid standard base64"},
		{"malformed headers", `{"version":1,"app":"demo","request":{"url":"https://example.com","headers":["Authorization secret-value"]}}`, "valid Name:Value pairs"},
		{"oversized body", string(tooLargeBody), "request body must not exceed"},
		{"project without environment", `{"version":1,"project":"shop","app":"demo","request":{"url":"https://example.com"}}`, "project and environment must be supplied together"},
		{"environment without project", `{"version":1,"environment":"staging","app":"demo","request":{"url":"https://example.com"}}`, "project and environment must be supplied together"},
		{"invalid environment", `{"version":1,"project":"shop","environment":"default","app":"demo","request":{"url":"https://example.com"}}`, "environment slug is invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := edgeruletrace.ParseScenarioConfig([]byte(tc.config))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseScenarioConfig error = %v, want substring %q", err, tc.want)
			}
			if tc.name == "malformed headers" && strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("header value leaked through config error: %v", err)
			}
		})
	}
}

func stringPointer(value string) *string { return &value }

func TestSimulateValidateRuleWithRequestBody(t *testing.T) {
	rule := validateTraceRule(t, "validate", api.ValidateModeBlock, `{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"]}`, nil, 0)
	input := validateTraceInput(`{"count":3}`)
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if !result.BodyProvided || result.BodyBytes != len(input.Body) {
		t.Fatalf("body metadata = provided:%v bytes:%d", result.BodyProvided, result.BodyBytes)
	}
	if result.Simulation.Status != "complete" || result.Simulation.Outcome != "continue" || len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].Outcome != "validated" {
		t.Fatalf("simulation = %#v", result.Simulation)
	}
}

func TestSimulateValidateRuleBlockDoesNotExposeSubmittedBody(t *testing.T) {
	rule := validateTraceRule(t, "validate-rule", api.ValidateModeBlock, `{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"]}`, nil, 0)
	input := validateTraceInput(`{"count":"private-value"}`)
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Status != "complete" || result.Simulation.Outcome != "validation_failed" || result.Simulation.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("simulation = %#v", result.Simulation)
	}
	step := result.Simulation.Steps[0]
	if step.ValidationField != "/count" || step.ValidationKeyword != "type" {
		t.Fatalf("validation details = %#v", step)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(encoded), "private-value") {
		t.Fatalf("result leaked submitted request body: %s", encoded)
	}
}

func TestSimulateValidateRuleModes(t *testing.T) {
	for _, tc := range []struct {
		mode       string
		outcome    string
		wantHeader bool
	}{
		{mode: api.ValidateModeObserve, outcome: "validation_failed_observe"},
		{mode: api.ValidateModeWarn, outcome: "validation_failed_warn", wantHeader: true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			rule := validateTraceRule(t, "warn-rule", tc.mode, `{"type":"integer"}`, nil, 0)
			result, err := edgeruletrace.Simulate(validateTraceInput(`"not-an-integer"`), []api.EdgeRuleResponse{rule})
			if err != nil {
				t.Fatalf("Simulate: %v", err)
			}
			if result.Simulation.Status != "complete" || result.Simulation.Outcome != "continue" || result.Simulation.Steps[0].Outcome != tc.outcome {
				t.Fatalf("simulation = %#v", result.Simulation)
			}
			if tc.wantHeader {
				if len(result.Simulation.ResponseHeaderOps) != 1 || result.Simulation.ResponseHeaderOps[0].Name != "X-Validation-Warning" || result.Simulation.ResponseHeaderOps[0].Value != "warn-rule" {
					t.Fatalf("response headers = %#v", result.Simulation.ResponseHeaderOps)
				}
			} else if len(result.Simulation.ResponseHeaderOps) != 0 {
				t.Fatalf("observe mode added response headers: %#v", result.Simulation.ResponseHeaderOps)
			}
		})
	}
}

func TestSimulateValidateRuleReportsMissingBodyAndContentType(t *testing.T) {
	rule := validateTraceRule(t, "validate", api.ValidateModeBlock, `{"type":"object"}`, []string{"application/json"}, 0)
	input := validateTraceInput("")
	input.BodyProvided = false
	input.Headers.Set("Content-Type", "text/plain")
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate wrong Content-Type: %v", err)
	}
	if result.Simulation.Outcome != "unsupported_media_type" || result.Simulation.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong Content-Type simulation = %#v", result.Simulation)
	}

	input.Headers.Set("Content-Type", "application/json; charset=utf-8")
	result, err = edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate missing body: %v", err)
	}
	if result.Simulation.Status != "incomplete" || result.Simulation.Outcome != "needs_request_body" {
		t.Fatalf("missing body simulation = %#v", result.Simulation)
	}
}

func TestSimulateValidateRuleAppliesBodyLimits(t *testing.T) {
	rule := validateTraceRule(t, "validate", api.ValidateModeBlock, `{"type":"string"}`, nil, 4)
	input := validateTraceInput(`"12345"`)
	input.RequestBodyMaxBytes = 16
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate rule cap: %v", err)
	}
	if result.Simulation.Outcome != "body_too_large" || result.Simulation.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("rule-cap simulation = %#v", result.Simulation)
	}

	rule = validateTraceRule(t, "validate", api.ValidateModeBlock, `{"type":"string"}`, nil, 0)
	input.RequestBodyMaxBytes = 4
	result, err = edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate plan cap: %v", err)
	}
	if result.Simulation.Outcome != "body_too_large" || result.Simulation.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("plan-cap simulation = %#v", result.Simulation)
	}
}

func TestNormalizeInputRejectsOversizedTraceBody(t *testing.T) {
	input := validateTraceInput(strings.Repeat("x", edgeruletrace.MaxTraceBodyBytes+1))
	if _, err := edgeruletrace.NormalizeInput(input); err == nil || !strings.Contains(err.Error(), "request body must not exceed") {
		t.Fatalf("NormalizeInput error = %v, want body-size rejection", err)
	}
}

func TestNormalizeInputPreservesEffectivePlanBodyLimit(t *testing.T) {
	input := validateTraceInput("body")
	input.RequestBodyMaxBytes = 250 << 20
	normalized, err := edgeruletrace.NormalizeInput(input)
	if err != nil {
		t.Fatalf("NormalizeInput: %v", err)
	}
	if normalized.RequestBodyMaxBytes != input.RequestBodyMaxBytes {
		t.Fatalf("effective plan request cap = %d, want %d", normalized.RequestBodyMaxBytes, input.RequestBodyMaxBytes)
	}
}

func TestParseRequestHeadersTrimsHTTPOptionalWhitespace(t *testing.T) {
	headers, err := edgeruletrace.ParseRequestHeaders([]string{
		"Origin: \thttps://app.example.com ",
		"X-Mode: exact value",
	})
	if err != nil {
		t.Fatalf("ParseRequestHeaders: %v", err)
	}
	if got := headers.Get("Origin"); got != "https://app.example.com" {
		t.Fatalf("Origin = %q, want trimmed field value", got)
	}
	if got := headers.Get("X-Mode"); got != "exact value" {
		t.Fatalf("X-Mode = %q, want interior whitespace retained", got)
	}
}

func TestSimulateRedactsSensitiveHeadersAcrossTraceOutput(t *testing.T) {
	const (
		authorization = "Bearer authorization-sentinel-83912"
		cookie        = "session=cookie-sentinel-83912"
		apiKey        = "api-key-sentinel-83912"
		sessionToken  = "custom-token-sentinel-83912"
		requestSet    = "action-session-sentinel-83912"
		responseSet   = "response-cookie-sentinel-83912"
		selector      = "mismatch-selector-sentinel-83912"
	)
	rules := []api.EdgeRuleResponse{
		{
			ID: "header-rule", Enabled: true, Kind: "headers", MatchHost: "*", MatchPath: "*", Priority: 10,
			MatchHeaders: map[string]string{"authorization": authorization},
			Action:       json.RawMessage(`{"headers":{"request_headers":[{"name":"X-Session-Token","value":"` + requestSet + `","action":"set"}],"response_headers":[{"name":"Set-Cookie","value":"` + responseSet + `","action":"set"}]}}`),
		},
		{
			ID: "mismatch-rule", Enabled: true, Kind: "rewrite", MatchHost: "*", MatchPath: "*", Priority: 20,
			MatchHeaders: map[string]string{"cookie": selector},
			Action:       json.RawMessage(`{"rewrite":{"from":"/old","to":"/new"}}`),
		},
	}
	input := edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet, AppMaintenanceLoaded: true,
		Headers: http.Header{
			"Authorization":   []string{authorization},
			"Cookie":          []string{cookie},
			"X-Api-Key":       []string{apiKey},
			"X-Session-Token": []string{sessionToken},
			"X-Region":        []string{"west"},
		},
	}
	result, err := edgeruletrace.Simulate(input, rules)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Rules[0].Status != "first_candidate" || result.Simulation.Outcome != "continue" {
		t.Fatalf("raw header matching changed by redaction: row=%#v simulation=%#v", result.Rules[0], result.Simulation)
	}
	if result.Rules[1].Status != "skipped" || !strings.Contains(result.Rules[1].Reason, "[REDACTED]") {
		t.Fatalf("sensitive mismatch reason = %q", result.Rules[1].Reason)
	}
	if result.Headers["authorization"][0] != "[REDACTED]" || result.Headers["cookie"][0] != "[REDACTED]" || result.Headers["x-api-key"][0] != "[REDACTED]" {
		t.Fatalf("top-level request headers were not redacted: %#v", result.Headers)
	}
	if result.Headers["x-region"][0] != "west" {
		t.Fatalf("ordinary request header was changed: %#v", result.Headers)
	}
	if result.Simulation.RequestHeaders["x-session-token"][0] != "[REDACTED]" {
		t.Fatalf("simulated request headers were not redacted: %#v", result.Simulation.RequestHeaders)
	}
	if got := result.Simulation.Steps[0].RequestOps[0].Value; got != "[REDACTED]" {
		t.Fatalf("request header action value = %q", got)
	}
	if got := result.Simulation.Steps[0].ResponseOps[0].Value; got != "[REDACTED]" {
		t.Fatalf("response header action value = %q", got)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, secret := range []string{authorization, cookie, apiKey, sessionToken, requestSet, responseSet, selector} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("serialized trace leaked %q: %s", secret, encoded)
		}
	}
}

func TestRedactHeaderInputForDisplay(t *testing.T) {
	got := edgeruletrace.RedactHeaderInputForDisplay("Authorization: Bearer form-auth-sentinel\r\nX-Session-Token:\tform-token-sentinel\nIdempotency-Key: order-opaque\nX-Region: west\nbroken form-cookie-sentinel")
	want := "Authorization: [REDACTED]\r\nX-Session-Token:\t[REDACTED]\nIdempotency-Key: [REDACTED]\nX-Region: west\n[REDACTED]"
	if got != want {
		t.Fatalf("RedactHeaderInputForDisplay() = %q, want %q", got, want)
	}
}

func TestSimulateInlineCORSRuleAppliesResponseHeaders(t *testing.T) {
	rule := corsTraceRule(t, "cors-rule", []string{"GET"}, api.EdgeRuleCORSAction{
		AllowOrigins: []string{"https://app.example.com"}, AllowMethods: []string{"GET", "POST"},
		AllowHeaders: []string{"Content-Type", "X-Request-ID"}, ExposeHeaders: []string{"X-Request-ID"},
		AllowCredentials: true, MaxAgeSeconds: 600,
	})
	input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet, Headers: http.Header{"Origin": []string{"https://app.example.com"}}, AppMaintenanceLoaded: true}
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	want := []api.EdgeRuleHeaderOp{
		{Action: "set", Name: "Access-Control-Allow-Origin", Value: "https://app.example.com"},
		{Action: "set", Name: "Access-Control-Allow-Credentials", Value: "true"},
		{Action: "set", Name: "Access-Control-Allow-Methods", Value: "GET, POST"},
		{Action: "set", Name: "Access-Control-Allow-Headers", Value: "Content-Type, X-Request-ID"},
		{Action: "set", Name: "Access-Control-Expose-Headers", Value: "X-Request-ID"},
		{Action: "set", Name: "Access-Control-Max-Age", Value: "600"},
	}
	if result.Simulation.Status != "complete" || result.Simulation.Outcome != "continue" || len(result.Simulation.ResponseHeaderOps) != len(want) {
		t.Fatalf("simulation = %#v", result.Simulation)
	}
	for i := range want {
		if result.Simulation.ResponseHeaderOps[i] != want[i] {
			t.Fatalf("response header ops = %#v, want %#v", result.Simulation.ResponseHeaderOps, want)
		}
	}
	if got := result.Simulation.Steps[0].Outcome; got != "cors_applied" {
		t.Fatalf("CORS step outcome = %q, want cors_applied", got)
	}
}

func TestSimulateAppMaintenanceGatePrecedesEdgeRulePhases(t *testing.T) {
	input := edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet,
		AppMaintenanceLoaded: true, AppMaintenanceMode: true,
	}
	rules := []api.EdgeRuleResponse{
		{ID: "maintenance-rule", Enabled: true, Kind: "maintenance", MatchHost: "*", MatchPath: "*", Priority: 10,
			Action: json.RawMessage(`{"maintenance":{"message":"route maintenance","retry_after_seconds":120}}`)},
		{ID: "redirect-rule", Enabled: true, Kind: "redirect", MatchHost: "*", MatchPath: "*", Priority: 10,
			Action: json.RawMessage(`{"redirect":{"to":"/new"}}`)},
	}
	result, err := edgeruletrace.Simulate(input, rules)
	if err != nil {
		t.Fatalf("Simulate app maintenance: %v", err)
	}
	if result.Simulation.Status != "complete" || result.Simulation.Outcome != "app_maintenance" || result.Simulation.StoppedAt != "app_maintenance" || result.Simulation.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("app maintenance simulation = %#v", result.Simulation)
	}
	if result.Simulation.ProblemCode != api.CodeAppMaintenance || result.Simulation.RetryAfterSeconds != api.EdgeRuleMaintenanceRetryAfterSeconds || result.Simulation.Message != `App "demo" is in maintenance mode` {
		t.Fatalf("app maintenance response = %#v", result.Simulation)
	}
	if len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].Kind != "app_maintenance" || result.Simulation.Steps[0].ProblemCode != api.CodeAppMaintenance {
		t.Fatalf("app maintenance steps = %#v", result.Simulation.Steps)
	}

	input.AppMaintenanceMode = false
	result, err = edgeruletrace.Simulate(input, rules)
	if err != nil {
		t.Fatalf("Simulate disabled app maintenance: %v", err)
	}
	if result.Simulation.Outcome != "maintenance" || result.Simulation.RetryAfterSeconds != 120 || len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].RuleID != "maintenance-rule" {
		t.Fatalf("edge-rule maintenance did not run after disabled app gate: %#v", result.Simulation)
	}
}

func TestSimulateAppMaintenanceRequiresMetadataAndFollowsRouting(t *testing.T) {
	rule := api.EdgeRuleResponse{ID: "maintenance", Enabled: true, Kind: "maintenance", MatchHost: "*", MatchPath: "*",
		Action: json.RawMessage(`{"maintenance":{"message":"down"}}`)}
	input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet}
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate without app metadata: %v", err)
	}
	if result.Simulation.Status != "incomplete" || result.Simulation.Outcome != "needs_app_maintenance" || result.Simulation.StoppedAt != "app_maintenance" {
		t.Fatalf("missing metadata simulation = %#v", result.Simulation)
	}
	if len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].Kind != "app_maintenance" {
		t.Fatalf("missing metadata steps = %#v", result.Simulation.Steps)
	}

	route := api.EdgeRuleResponse{ID: "route", Enabled: true, Kind: "route", MatchHost: "*", MatchPath: "*",
		Action: json.RawMessage(`{"route":{"target_app_slug":"other-app"}}`)}
	input.AppMaintenanceLoaded, input.AppMaintenanceMode = true, true
	result, err = edgeruletrace.Simulate(input, []api.EdgeRuleResponse{route})
	if err != nil {
		t.Fatalf("Simulate route before app maintenance: %v", err)
	}
	if result.Simulation.Status != "incomplete" || result.Simulation.Outcome != "route" || result.Simulation.TargetApp != "other-app" {
		t.Fatalf("route should precede target-app maintenance metadata: %#v", result.Simulation)
	}
}

func TestApplyEnvironmentRoutePolicyOwnsEmptyAllowlist(t *testing.T) {
	input := edgeruletrace.Input{
		Project: "shop", Environment: "staging", App: "demo", Host: "staging.example.com",
		Path: "/previously-allowed", Method: http.MethodGet, AppMaintenanceLoaded: true,
		OnlyAllowDeclaredRoutes: true, DeclaredRouteDocumentLoaded: true,
		DeclaredRouteOpenAPIDoc: []byte("openapi: 3.1.0\ninfo:\n  title: demo\n  version: v1\npaths:\n  /previously-allowed:\n    get:\n      responses: {}\n"),
	}
	got, err := edgeruletrace.ApplyEnvironmentRoutePolicy(input, api.ProjectEnvironmentRoutePolicyResponse{
		Ownership: "environment", OnlyAllowDeclaredRoutes: true, DeclaredRoutes: []api.DeclaredRoute{},
	})
	if err != nil {
		t.Fatalf("ApplyEnvironmentRoutePolicy: %v", err)
	}
	if !got.DeclaredRoutesLoaded || got.DeclaredRouteDocumentLoaded || got.DeclaredRouteOpenAPIDoc != nil {
		t.Fatalf("environment route context = %#v", got)
	}
	result, err := edgeruletrace.Simulate(got, nil)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Outcome != "undeclared_route" || result.Simulation.StatusCode != http.StatusNotFound {
		t.Fatalf("empty environment allowlist fell through: %#v", result.Simulation)
	}
}

func TestApplyEnvironmentEdgePolicyReplacesOnlyHeadersAndCORS(t *testing.T) {
	input, err := edgeruletrace.NormalizeInput(edgeruletrace.Input{
		Project: "shop", Environment: "staging", App: "demo", Host: "staging.example.com", Path: "/",
		Method: http.MethodGet, AppMaintenanceLoaded: true,
	})
	if err != nil {
		t.Fatalf("NormalizeInput: %v", err)
	}
	rules := []api.EdgeRuleResponse{
		{ID: "global-headers", Enabled: true, Kind: "headers", MatchHost: "*", MatchPath: "*",
			Action: json.RawMessage(`{"headers":{"request_headers":[{"name":"X-Environment","action":"set","value":"global"}]}}`)},
		{ID: "global-cors", Enabled: true, Kind: "cors", MatchHost: "*", MatchPath: "*",
			Action: json.RawMessage(`{"cors":{"allow_origins":["https://global.example.com"],"allow_methods":["GET"]}}`)},
		{ID: "shared-respond", Enabled: true, Kind: "respond", MatchHost: "*", MatchPath: "*", MatchHeaders: map[string]string{"x-environment": "staging"},
			Action: json.RawMessage(`{"respond":{"status_code":202,"body":{"ok":true}}}`)},
	}
	policy := api.ProjectEnvironmentEdgePolicyResponse{
		Ownership: "environment",
		Rules: []api.ProjectEnvironmentEdgeRuleResponse{{
			Kind: "headers", MatchPath: "*", Priority: 10, Enabled: true,
			Action: json.RawMessage(`{"headers":{"request_headers":[{"name":"X-Environment","action":"set","value":"staging"}]}}`),
		}},
	}
	effective, err := edgeruletrace.ApplyEnvironmentEdgePolicy(input, rules, policy, "https://staging.example.com", nil)
	if err != nil {
		t.Fatalf("ApplyEnvironmentEdgePolicy: %v", err)
	}
	if len(effective) != 2 || effective[0].ID != "shared-respond" || effective[1].ID != "environment/staging/demo/1" {
		t.Fatalf("effective rules = %#v; global headers/CORS should be replaced", effective)
	}
	result, err := edgeruletrace.Simulate(input, effective)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Project != "shop" || result.Environment != "staging" || result.Simulation.Outcome != "fixed_response" || result.Simulation.StatusCode != http.StatusAccepted {
		t.Fatalf("environment simulation = %#v", result)
	}
	if got := result.Simulation.RequestHeaders["x-environment"]; len(got) != 1 || got[0] != "staging" {
		t.Fatalf("effective request headers = %#v", result.Simulation.RequestHeaders)
	}
}

func TestApplyEnvironmentEdgePolicyRequiresAHostOwnedBySelectedEnvironment(t *testing.T) {
	input, err := edgeruletrace.NormalizeInput(edgeruletrace.Input{
		Project: "shop", Environment: "staging", App: "demo", Host: "prod.example.com", Path: "/",
	})
	if err != nil {
		t.Fatalf("NormalizeInput: %v", err)
	}
	policy := api.ProjectEnvironmentEdgePolicyResponse{Ownership: "application"}
	if _, err := edgeruletrace.ApplyEnvironmentEdgePolicy(input, nil, policy, "https://staging.example.com", nil); err == nil {
		t.Fatal("mismatched environment host was accepted")
	}
	if _, err := edgeruletrace.ApplyEnvironmentEdgePolicy(input, nil, policy, "", []api.ProjectEnvironmentDomainResponse{{Domain: "prod.example.com", Verified: false}}); err == nil {
		t.Fatal("unverified custom domain was accepted")
	}
	if _, err := edgeruletrace.ApplyEnvironmentEdgePolicy(input, nil, policy, "", []api.ProjectEnvironmentDomainResponse{{Domain: "prod.example.com", Verified: true}}); err != nil {
		t.Fatalf("verified environment domain was rejected: %v", err)
	}

	input.Host = "api.example.com"
	if _, err := edgeruletrace.ApplyEnvironmentEdgePolicy(input, nil, policy, "", []api.ProjectEnvironmentDomainResponse{{Domain: "*.example.com", Verified: true}}); err != nil {
		t.Fatalf("verified wildcard environment domain was rejected: %v", err)
	}
	input.Host = "example.com"
	if _, err := edgeruletrace.ApplyEnvironmentEdgePolicy(input, nil, policy, "", []api.ProjectEnvironmentDomainResponse{{Domain: "*.example.com", Verified: true}}); err == nil {
		t.Fatal("wildcard environment domain unexpectedly covered its apex")
	}
}

func TestSimulateDeclaredRoutesMatchOpenAPIStylePathsAndMethods(t *testing.T) {
	input := edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/users/42/", Method: http.MethodHead,
		AppMaintenanceLoaded: true, OnlyAllowDeclaredRoutes: true,
		DeclaredRoutes: []api.DeclaredRoute{{Path: "/users/{user_id}", Methods: []string{"get"}}},
	}
	result, err := edgeruletrace.Simulate(input, nil)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Status != "complete" || result.Simulation.Outcome != "continue" || len(result.Simulation.Steps) != 1 {
		t.Fatalf("simulation = %#v", result.Simulation)
	}
	step := result.Simulation.Steps[0]
	if step.Phase != "declared_routes" || step.Outcome != "allowed" || step.PathBefore != "/users/42/" || step.PathAfter != "/users/42/" {
		t.Fatalf("declared-route step = %#v", step)
	}
}

func TestSimulateDeclaredRouteGateUsesOriginalPathAfterRewrite(t *testing.T) {
	rewrite := api.EdgeRuleResponse{
		ID: "rewrite", Enabled: true, Kind: "rewrite", MatchHost: "*", MatchPath: "/legacy/*",
		Action: json.RawMessage(`{"rewrite":{"from":"/legacy","to":"/internal"}}`),
	}
	input := edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/legacy/42", Method: http.MethodGet,
		AppMaintenanceLoaded: true, OnlyAllowDeclaredRoutes: true,
		DeclaredRoutes: []api.DeclaredRoute{{Path: "/internal/{id}", Methods: []string{"GET"}}},
	}
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rewrite})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Outcome != "undeclared_route" || result.Simulation.StatusCode != http.StatusNotFound || result.Simulation.ProblemCode != api.CodeUndeclaredRoute {
		t.Fatalf("simulation = %#v", result.Simulation)
	}
	if result.Simulation.FinalPath != "/internal/42" || result.Simulation.Message != "GET /legacy/42 is not declared for this app" {
		t.Fatalf("declared-route response = %#v", result.Simulation)
	}
	if len(result.Simulation.Steps) != 2 || result.Simulation.Steps[0].Phase != "rewrite" || result.Simulation.Steps[1].PathBefore != "/legacy/42" || result.Simulation.Steps[1].PathAfter != "/internal/42" {
		t.Fatalf("simulation steps = %#v", result.Simulation.Steps)
	}
}

func TestSimulateDeclaredRouteRejectsUndeclaredMethodAndPath(t *testing.T) {
	for _, tc := range []struct {
		name, path, method string
	}{
		{name: "path", path: "/admin", method: http.MethodGet},
		{name: "method", path: "/health", method: http.MethodPost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := edgeruletrace.Input{
				App: "demo", Host: "example.com", Path: tc.path, Method: tc.method,
				AppMaintenanceLoaded: true, OnlyAllowDeclaredRoutes: true,
				DeclaredRoutes: []api.DeclaredRoute{{Path: "/health", Methods: []string{"GET"}}},
			}
			result, err := edgeruletrace.Simulate(input, nil)
			if err != nil {
				t.Fatalf("Simulate: %v", err)
			}
			if result.Simulation.Status != "complete" || result.Simulation.Outcome != "undeclared_route" || result.Simulation.StatusCode != http.StatusNotFound || result.Simulation.ProblemCode != api.CodeUndeclaredRoute || result.Simulation.Message != tc.method+" "+tc.path+" is not declared for this app" {
				t.Fatalf("simulation = %#v", result.Simulation)
			}
			if len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].StatusCode != http.StatusNotFound || result.Simulation.Steps[0].ProblemCode != api.CodeUndeclaredRoute {
				t.Fatalf("declared-route step = %#v", result.Simulation.Steps)
			}
		})
	}
}

func TestSimulateDeclaredRouteOpenAPIFallbackAndUnavailablePolicy(t *testing.T) {
	base := edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/widgets/42", Method: http.MethodHead,
		AppMaintenanceLoaded: true, OnlyAllowDeclaredRoutes: true,
	}
	base.DeclaredRouteDocumentLoaded = true
	base.DeclaredRouteOpenAPIDoc = []byte(`openapi: 3.1.0
info:
  title: demo
  version: v1
paths:
  /widgets/{id}:
    get:
      responses: {}
`)
	result, err := edgeruletrace.Simulate(base, nil)
	if err != nil {
		t.Fatalf("Simulate OpenAPI fallback: %v", err)
	}
	if result.Simulation.Outcome != "continue" || len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].Outcome != "allowed" {
		t.Fatalf("OpenAPI fallback = %#v", result.Simulation)
	}

	base.DeclaredRouteDocumentLoaded = false
	base.DeclaredRouteOpenAPIDoc = nil
	result, err = edgeruletrace.Simulate(base, nil)
	if err != nil {
		t.Fatalf("Simulate unavailable document: %v", err)
	}
	if result.Simulation.Status != "incomplete" || result.Simulation.Outcome != "needs_declared_route_policy" {
		t.Fatalf("unloaded document = %#v", result.Simulation)
	}

	base.DeclaredRouteDocumentMissing = true
	result, err = edgeruletrace.Simulate(base, nil)
	if err != nil {
		t.Fatalf("Simulate missing document: %v", err)
	}
	if result.Simulation.Status != "complete" || result.Simulation.Outcome != "declared_route_policy_unavailable" || result.Simulation.StatusCode != http.StatusServiceUnavailable || result.Simulation.ProblemCode != api.CodeDeclaredRoutePolicyUnavailable {
		t.Fatalf("missing document = %#v", result.Simulation)
	}
}

func TestSimulateDeclaredRouteGateFollowsCORSPreflight(t *testing.T) {
	rule := corsTraceRule(t, "cors-rule", []string{"GET"}, api.EdgeRuleCORSAction{
		AllowOrigins: []string{"https://app.example.com"}, AllowMethods: []string{"GET"}, AllowHeaders: []string{"Content-Type"},
	})
	input := edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/not-declared", Method: http.MethodOptions,
		Headers:              http.Header{"Origin": []string{"https://app.example.com"}, "Access-Control-Request-Method": []string{"GET"}},
		AppMaintenanceLoaded: true, OnlyAllowDeclaredRoutes: true, DeclaredRouteDocumentMissing: true,
	}
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Outcome != "cors_preflight" || result.Simulation.StatusCode != http.StatusNoContent || result.Simulation.StoppedAt != "cors" {
		t.Fatalf("CORS preflight did not short-circuit route gate: %#v", result.Simulation)
	}
}

func TestSimulateAppCORSDefaultFallback(t *testing.T) {
	enabled := true
	for _, tc := range []struct {
		name   string
		method string
	}{
		{name: "ordinary request", method: http.MethodGet},
		{name: "preflight continues to app", method: http.MethodOptions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := http.Header{"Origin": []string{"https://app.example.com"}}
			if tc.method == http.MethodOptions {
				headers.Set("Access-Control-Request-Method", http.MethodGet)
			}
			input := edgeruletrace.Input{
				App: "demo", Host: "example.com", Path: "/", Method: tc.method, Headers: headers,
				AppMaintenanceLoaded:  true,
				AppCORSDefaultsLoaded: true, CORSDefaultEnabled: &enabled,
				CORSDefaultOrigins: []string{"https://app.example.com"},
			}
			result, err := edgeruletrace.Simulate(input, nil)
			if err != nil {
				t.Fatalf("Simulate: %v", err)
			}
			if result.Simulation.Status != "complete" || result.Simulation.Outcome != "continue" || result.Simulation.StatusCode != 0 || len(result.Simulation.Steps) != 1 {
				t.Fatalf("simulation = %#v", result.Simulation)
			}
			step := result.Simulation.Steps[0]
			if step.Kind != "app_default_cors" || step.Outcome != "cors_default_applied" || len(step.ResponseOps) != 4 {
				t.Fatalf("CORS default step = %#v", step)
			}
			if tc.method == http.MethodOptions && !strings.Contains(step.Reason, "not short-circuited") {
				t.Fatalf("preflight reason = %q, want pass-through explanation", step.Reason)
			}
			want := []api.EdgeRuleHeaderOp{
				{Action: "set", Name: "Access-Control-Allow-Origin", Value: "https://app.example.com"},
				{Action: "set", Name: "Access-Control-Allow-Methods", Value: "GET, POST, OPTIONS"},
				{Action: "set", Name: "Access-Control-Allow-Headers", Value: "*"},
				{Action: "set", Name: "Access-Control-Expose-Headers", Value: "Streaming-Status, Streaming-Status-Accept-Hint"},
			}
			if len(result.Simulation.ResponseHeaderOps) != len(want) {
				t.Fatalf("response header ops = %#v, want %#v", result.Simulation.ResponseHeaderOps, want)
			}
			for i := range want {
				if result.Simulation.ResponseHeaderOps[i] != want[i] || step.ResponseOps[i] != want[i] {
					t.Fatalf("response header ops = %#v, step ops = %#v; want %#v", result.Simulation.ResponseHeaderOps, step.ResponseOps, want)
				}
			}
		})
	}
}

func TestSimulateAppCORSDefaultIncompleteWithoutAppMetadata(t *testing.T) {
	input := edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet,
		Headers:              http.Header{"Origin": []string{"https://app.example.com"}},
		AppMaintenanceLoaded: true,
	}
	result, err := edgeruletrace.Simulate(input, nil)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Status != "incomplete" || result.Simulation.Outcome != "needs_app_cors_defaults" || result.Simulation.StoppedAt != "cors" || result.Simulation.Steps[0].Kind != "app_default_cors" {
		t.Fatalf("simulation = %#v", result.Simulation)
	}
}

func TestSimulateAppCORSDefaultMissesAndEdgeRulePrecedence(t *testing.T) {
	allowed, disabled := true, false
	for _, tc := range []struct {
		name        string
		enabled     *bool
		origins     []string
		origin      string
		wantOutcome string
	}{
		{name: "disabled", enabled: &disabled, origins: []string{"https://app.example.com"}, origin: "https://app.example.com", wantOutcome: "cors_default_disabled"},
		{name: "empty allowlist", enabled: &allowed, origin: "https://app.example.com", wantOutcome: "cors_default_disabled"},
		{name: "origin denied", enabled: &allowed, origins: []string{"https://other.example.com"}, origin: "https://app.example.com", wantOutcome: "cors_default_origin_not_allowed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := edgeruletrace.Input{
				App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet,
				Headers:               http.Header{"Origin": []string{tc.origin}},
				AppMaintenanceLoaded:  true,
				AppCORSDefaultsLoaded: true, CORSDefaultEnabled: tc.enabled, CORSDefaultOrigins: tc.origins,
			}
			result, err := edgeruletrace.Simulate(input, nil)
			if err != nil {
				t.Fatalf("Simulate: %v", err)
			}
			if result.Simulation.Status != "complete" || result.Simulation.Outcome != "continue" || len(result.Simulation.ResponseHeaderOps) != 0 || len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].Outcome != tc.wantOutcome {
				t.Fatalf("simulation = %#v", result.Simulation)
			}
		})
	}

	input := edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet,
		Headers:               http.Header{"Origin": []string{"https://app.example.com"}},
		AppMaintenanceLoaded:  true,
		AppCORSDefaultsLoaded: true, CORSDefaultEnabled: &allowed,
		CORSDefaultOrigins: []string{"https://app.example.com"},
	}
	rule := corsTraceRule(t, "cors-rule", []string{"GET"}, api.EdgeRuleCORSAction{
		AllowOrigins: []string{"https://other.example.com"}, AllowMethods: []string{"GET"},
	})
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate edge-rule precedence: %v", err)
	}
	if result.Simulation.Status != "complete" || result.Simulation.Outcome != "continue" || len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].Outcome != "cors_origin_not_allowed" || len(result.Simulation.ResponseHeaderOps) != 0 {
		t.Fatalf("matching edge-rule CORS must suppress app-default fallback: %#v", result.Simulation)
	}
}

func TestRequiresAppCORSDefaultData(t *testing.T) {
	if edgeruletrace.RequiresAppCORSDefaultData(nil) || edgeruletrace.RequiresAppCORSDefaultData(http.Header{"Origin": []string{""}}) {
		t.Fatal("empty Origin should not require app CORS metadata")
	}
	if !edgeruletrace.RequiresAppCORSDefaultData(http.Header{"Origin": []string{"https://app.example.com"}}) {
		t.Fatal("non-empty Origin should require app CORS metadata")
	}
}

func TestSimulateCORSPreflightUsesRequestedMethodToSelectRule(t *testing.T) {
	rule := corsTraceRule(t, "cors-get", []string{"GET"}, api.EdgeRuleCORSAction{
		AllowOrigins: []string{"https://app.example.com"}, AllowMethods: []string{"GET"}, AllowHeaders: []string{"Content-Type"},
	})
	input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/", Method: http.MethodOptions, Headers: http.Header{
		"Origin": []string{"https://app.example.com"}, "Access-Control-Request-Method": []string{"GET"},
	}, AppMaintenanceLoaded: true}
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if len(result.Rules) != 1 || result.Rules[0].Status != "first_candidate" || !strings.Contains(result.Rules[0].Reason, "preflight requests GET") {
		t.Fatalf("standalone CORS row = %#v", result.Rules)
	}
	if result.Simulation.Status != "complete" || result.Simulation.Outcome != "cors_preflight" || result.Simulation.StatusCode != http.StatusNoContent || result.Simulation.StoppedAt != "cors" {
		t.Fatalf("simulation = %#v", result.Simulation)
	}
	if len(result.Simulation.ResponseHeaderOps) != 3 || result.Simulation.Steps[0].StatusCode != http.StatusNoContent {
		t.Fatalf("preflight response = %#v", result.Simulation)
	}
}

func TestSimulateCORSPreflightMethodSelectorRemainsCaseSensitive(t *testing.T) {
	rule := corsTraceRule(t, "cors-get", []string{"GET"}, api.EdgeRuleCORSAction{
		AllowOrigins: []string{"https://app.example.com"}, AllowMethods: []string{"GET"},
	})
	input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/", Method: http.MethodOptions, Headers: http.Header{
		"Origin": []string{"https://app.example.com"}, "Access-Control-Request-Method": []string{"get"},
	}, AppCORSDefaultsLoaded: true}
	input.AppMaintenanceLoaded = true
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Rules[0].Status != "skipped" || result.Simulation.Outcome != "continue" || len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].Outcome != "cors_default_disabled" {
		t.Fatalf("lowercase preflight method should not select GET CORS rule: rows=%#v simulation=%#v", result.Rules, result.Simulation)
	}
}

func TestSimulateCORSDeniedMethodAndOriginContinueWithoutHeaders(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		requestMethod string
		matchMethod   string
		allowMethods  []string
		allowOrigins  []string
		origin        string
		outcome       string
	}{
		{name: "method denied", method: http.MethodOptions, requestMethod: "PUT", matchMethod: "PUT", allowMethods: []string{"GET"}, allowOrigins: []string{"https://app.example.com"}, origin: "https://app.example.com", outcome: "cors_method_not_allowed"},
		{name: "origin denied", method: http.MethodGet, matchMethod: "GET", allowMethods: []string{"GET"}, allowOrigins: []string{"https://app.example.com"}, origin: "https://other.example.com", outcome: "cors_origin_not_allowed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule := corsTraceRule(t, "cors-rule", []string{tc.matchMethod}, api.EdgeRuleCORSAction{AllowMethods: tc.allowMethods, AllowOrigins: tc.allowOrigins})
			headers := http.Header{"Origin": []string{tc.origin}}
			if tc.requestMethod != "" {
				headers.Set("Access-Control-Request-Method", tc.requestMethod)
			}
			result, err := edgeruletrace.Simulate(edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/", Method: tc.method, Headers: headers, AppMaintenanceLoaded: true}, []api.EdgeRuleResponse{rule})
			if err != nil {
				t.Fatalf("Simulate: %v", err)
			}
			if result.Simulation.Status != "complete" || result.Simulation.Outcome != "continue" || len(result.Simulation.ResponseHeaderOps) != 0 || result.Simulation.Steps[0].Outcome != tc.outcome {
				t.Fatalf("simulation = %#v", result.Simulation)
			}
		})
	}
}

func TestSimulateCORSOptionsWithoutOriginShortCircuitsWithoutHeaders(t *testing.T) {
	rule := corsTraceRule(t, "cors-options", []string{"OPTIONS"}, api.EdgeRuleCORSAction{
		AllowOrigins: []string{"https://app.example.com"}, AllowMethods: []string{"GET"},
	})
	result, err := edgeruletrace.Simulate(edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/", Method: http.MethodOptions, AppMaintenanceLoaded: true}, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Outcome != "cors_preflight" || result.Simulation.StatusCode != http.StatusNoContent || len(result.Simulation.ResponseHeaderOps) != 0 {
		t.Fatalf("simulation = %#v", result.Simulation)
	}
}

func TestSimulateCORSPresetIsIncomplete(t *testing.T) {
	presetID := "preset-1"
	rule := corsTraceRule(t, "cors-preset", []string{"GET"}, api.EdgeRuleCORSAction{CorsPresetID: &presetID})
	result, err := edgeruletrace.Simulate(edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet, AppMaintenanceLoaded: true}, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Status != "incomplete" || result.Simulation.Outcome != "needs_cors_preset" || result.Simulation.StoppedAt != "cors" {
		t.Fatalf("simulation = %#v", result.Simulation)
	}
}

func TestSimulateCORSPresetUsesResolvedSettings(t *testing.T) {
	presetID := "preset-1"
	rule := corsTraceRule(t, "cors-preset", []string{"GET"}, api.EdgeRuleCORSAction{CorsPresetID: &presetID})
	rule.AccountID = "acct-1"
	input := edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet,
		Headers:              http.Header{"Origin": []string{"https://app.example.com"}},
		AppMaintenanceLoaded: true,
		CorsPresets: []api.CorsPresetResponse{{
			ID: presetID, AccountID: "acct-1", AllowOrigins: []string{"https://app.example.com"}, AllowMethods: []string{"GET", "POST"},
			AllowHeaders: []string{"Content-Type"}, ExposeHeaders: []string{"X-Request-ID"}, AllowCredentials: true, MaxAgeSeconds: 600,
		}},
	}
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Status != "complete" || result.Simulation.Outcome != "continue" || result.Simulation.Steps[0].Outcome != "cors_applied" {
		t.Fatalf("simulation = %#v", result.Simulation)
	}
	want := []api.EdgeRuleHeaderOp{
		{Action: "set", Name: "Access-Control-Allow-Origin", Value: "https://app.example.com"},
		{Action: "set", Name: "Access-Control-Allow-Credentials", Value: "true"},
		{Action: "set", Name: "Access-Control-Allow-Methods", Value: "GET, POST"},
		{Action: "set", Name: "Access-Control-Allow-Headers", Value: "Content-Type"},
		{Action: "set", Name: "Access-Control-Expose-Headers", Value: "X-Request-ID"},
		{Action: "set", Name: "Access-Control-Max-Age", Value: "600"},
	}
	if len(result.Simulation.ResponseHeaderOps) != len(want) {
		t.Fatalf("response header ops = %#v, want %#v", result.Simulation.ResponseHeaderOps, want)
	}
	for i := range want {
		if result.Simulation.ResponseHeaderOps[i] != want[i] {
			t.Fatalf("response header ops = %#v, want %#v", result.Simulation.ResponseHeaderOps, want)
		}
	}
}

func TestSimulateCORSPresetMergesRuleOverridesAndChecksAccount(t *testing.T) {
	presetID := "preset-1"
	rule := corsTraceRule(t, "cors-preset", []string{"GET"}, api.EdgeRuleCORSAction{
		CorsPresetID: &presetID, AllowMethods: []string{"PATCH"}, AllowHeaders: []string{"X-Rule"}, MaxAgeSeconds: 30,
	})
	rule.AccountID = "acct-1"
	input := edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet,
		Headers:              http.Header{"Origin": []string{"https://app.example.com"}},
		AppMaintenanceLoaded: true,
		CorsPresets: []api.CorsPresetResponse{{
			ID: presetID, AccountID: "acct-1", AllowOrigins: []string{"https://app.example.com"}, AllowMethods: []string{"GET"},
			AllowHeaders: []string{"Content-Type"}, AllowCredentials: true, MaxAgeSeconds: 600,
		}},
	}
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	ops := result.Simulation.ResponseHeaderOps
	if len(ops) != 5 || ops[2].Value != "PATCH" || ops[3].Value != "X-Rule" || ops[4].Value != "30" {
		t.Fatalf("rule overrides were not applied over preset: %#v", ops)
	}

	input.CorsPresets[0].AccountID = "another-account"
	result, err = edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate cross-account preset: %v", err)
	}
	if result.Simulation.Status != "incomplete" || result.Simulation.Outcome != "needs_cors_preset" {
		t.Fatalf("cross-account preset should remain unavailable: %#v", result.Simulation)
	}
}

func TestSimulateCORSPresetRejectsWildcardCredentialsMerge(t *testing.T) {
	presetID := "preset-1"
	rule := corsTraceRule(t, "cors-preset", []string{"GET"}, api.EdgeRuleCORSAction{CorsPresetID: &presetID})
	rule.AccountID = "acct-1"
	input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet, AppMaintenanceLoaded: true, CorsPresets: []api.CorsPresetResponse{{
		ID: presetID, AccountID: "acct-1", AllowOrigins: []string{"*"}, AllowMethods: []string{"GET"}, AllowCredentials: true,
	}}}
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Status != "incomplete" || result.Simulation.Outcome != "invalid_cors_preset_policy" {
		t.Fatalf("simulation = %#v", result.Simulation)
	}
}

func TestRequiresCorsPresetData(t *testing.T) {
	inline := corsTraceRule(t, "inline", []string{"GET"}, api.EdgeRuleCORSAction{AllowOrigins: []string{"*"}})
	presetID := "preset-1"
	preset := corsTraceRule(t, "preset", []string{"GET"}, api.EdgeRuleCORSAction{CorsPresetID: &presetID})
	if edgeruletrace.RequiresCorsPresetData([]api.EdgeRuleResponse{inline}) {
		t.Fatal("inline rule unexpectedly requires preset data")
	}
	preset.Enabled = false
	if edgeruletrace.RequiresCorsPresetData([]api.EdgeRuleResponse{preset}) {
		t.Fatal("disabled rule unexpectedly requires preset data")
	}
	preset.Enabled = true
	if !edgeruletrace.RequiresCorsPresetData([]api.EdgeRuleResponse{inline, preset}) {
		t.Fatal("preset-backed rule did not require preset data")
	}
}

func TestSimulateLimitRuleWithRequestBody(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		bodySet     bool
		planCap     int64
		buffered    int
		streaming   int
		status      string
		outcome     string
		stepOutcome string
		wantReason  string
		statusCode  int
	}{
		{name: "within buffered and streaming caps", body: "123", bodySet: true, planCap: 1 << 20, buffered: 4, streaming: 10, status: "complete", outcome: "continue", stepOutcome: "within_limit"},
		{name: "streaming cap clamps to platform limit", body: "123", bodySet: true, planCap: 250 << 20, buffered: 4, streaming: 200 << 20, status: "complete", outcome: "continue", stepOutcome: "within_limit", wantReason: "streaming limit (104857600 bytes)"},
		{name: "body exceeds buffered cap but streaming context is unknown", body: "12345", bodySet: true, planCap: 1 << 20, buffered: 4, streaming: 10, status: "incomplete", outcome: "needs_streaming_context", stepOutcome: "needs_streaming_context"},
		{name: "body exceeds both possible caps", body: "12345678901", bodySet: true, planCap: 1 << 20, buffered: 4, streaming: 10, status: "complete", outcome: "body_too_large", stepOutcome: "body_too_large", statusCode: http.StatusRequestEntityTooLarge},
		{name: "plan cap is lower than rule cap", body: "12345", bodySet: true, planCap: 4, buffered: 10, status: "complete", outcome: "body_too_large", stepOutcome: "body_too_large", statusCode: http.StatusRequestEntityTooLarge},
		{name: "missing body context", planCap: 1 << 20, buffered: 4, status: "incomplete", outcome: "needs_request_body", stepOutcome: "needs_request_body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := validateTraceInput(tc.body)
			input.BodyProvided = tc.bodySet
			input.RequestBodyMaxBytes = tc.planCap
			rule := limitTraceRule(t, "limit-rule", tc.buffered, tc.streaming)
			result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
			if err != nil {
				t.Fatalf("Simulate: %v", err)
			}
			if result.Simulation.Status != tc.status || result.Simulation.Outcome != tc.outcome || result.Simulation.StatusCode != tc.statusCode {
				t.Fatalf("simulation = %#v, want status=%q outcome=%q status_code=%d", result.Simulation, tc.status, tc.outcome, tc.statusCode)
			}
			if len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].Phase != "limit" {
				t.Fatalf("simulation steps = %#v, want a single limit evaluation", result.Simulation.Steps)
			}
			if result.Simulation.Steps[0].Outcome != tc.stepOutcome {
				t.Fatalf("limit step outcome = %q, want %q", result.Simulation.Steps[0].Outcome, tc.stepOutcome)
			}
			if tc.wantReason != "" && !strings.Contains(result.Simulation.Steps[0].Reason, tc.wantReason) {
				t.Fatalf("limit step reason = %q, want substring %q", result.Simulation.Steps[0].Reason, tc.wantReason)
			}
			if tc.outcome == "needs_streaming_context" && !strings.Contains(result.Simulation.Reason, "buffered limit is 4 bytes and streaming limit is 10 bytes") {
				t.Fatalf("ambiguous cap reason = %q", result.Simulation.Reason)
			}
		})
	}
}

func TestSimulateLimitRuleDoesNotExposeSubmittedBody(t *testing.T) {
	body := "private-limit-body"
	rule := limitTraceRule(t, "limit-rule", 4, 0)
	result, err := edgeruletrace.Simulate(validateTraceInput(body), []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Outcome != "body_too_large" || result.Simulation.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("simulation = %#v", result.Simulation)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(encoded), body) {
		t.Fatalf("result leaked submitted request body: %s", encoded)
	}
}

func TestSimulateCacheRuleReportsPolicyWithoutPredictingLookupResult(t *testing.T) {
	rule := cacheTraceRule(t, "cache-rule", api.EdgeRuleCacheAction{
		MaxAgeSeconds: 90, StaleWhileRevalidateSeconds: 30, StaleIfErrorSeconds: 120,
		VaryOn: []string{"Accept-Encoding", "Accept-Language"}, Methods: []string{"GET", "HEAD"},
	})
	rule.MatchMethods = []string{"GET"}
	input := edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/catalog", Method: http.MethodGet,
		AppMaintenanceLoaded: true,
	}
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Status != "incomplete" || result.Simulation.Outcome != "needs_cache_runtime_context" || result.Simulation.StoppedAt != "cache" {
		t.Fatalf("simulation = %#v", result.Simulation)
	}
	if len(result.Rules) != 1 || result.Rules[0].Outcome != "cache_lookup_candidate" || result.Rules[0].ActionPreview == nil {
		t.Fatalf("cache rule row = %#v", result.Rules)
	}
	policy := result.Rules[0].ActionPreview.CachePolicy
	if policy == nil || policy.RequestGate != "lookup_candidate" || policy.MaxAgeSeconds != 90 || policy.StaleWhileRevalidateSeconds != 30 || policy.StaleIfErrorSeconds != 120 {
		t.Fatalf("cache policy = %#v", policy)
	}
	if got := strings.Join(policy.VaryOn, ","); got != "Accept-Encoding,Accept-Language" {
		t.Fatalf("vary dimensions = %q, want deterministic order", got)
	}
	if got := strings.Join(policy.Methods, ","); got != "GET" {
		t.Fatalf("effective cache methods = %q, want intersection of action and rule selectors", got)
	}
	if len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].CachePolicy == nil || result.Simulation.Steps[0].CachePolicy.RequestGate != "lookup_candidate" {
		t.Fatalf("cache simulation step = %#v", result.Simulation.Steps)
	}
	if !strings.Contains(result.Simulation.Reason, "hit, miss, and stale outcomes are not inferred") {
		t.Fatalf("simulation does not explain unavailable cache state: %q", result.Simulation.Reason)
	}
}

func TestSimulateCacheRuleReportsDeterministicBypasses(t *testing.T) {
	rule := cacheTraceRule(t, "cache-rule", api.EdgeRuleCacheAction{
		MaxAgeSeconds: 60, Methods: []string{"GET"},
	})
	cases := []struct {
		name        string
		method      string
		headers     http.Header
		wantOutcome string
		wantGate    string
	}{
		{name: "method", method: http.MethodPost, wantOutcome: "cache_bypassed_method", wantGate: "bypassed_method"},
		{name: "authorization", method: http.MethodGet, headers: http.Header{"Authorization": []string{"Bearer cache-secret-123"}}, wantOutcome: "cache_bypassed_credentials", wantGate: "bypassed_credentials"},
		{name: "cookie", method: http.MethodGet, headers: http.Header{"Cookie": []string{"sid=private"}}, wantOutcome: "cache_bypassed_credentials", wantGate: "bypassed_credentials"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := edgeruletrace.Input{
				App: "demo", Host: "example.com", Path: "/catalog", Method: tc.method,
				Headers: tc.headers, AppMaintenanceLoaded: true,
			}
			result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
			if err != nil {
				t.Fatalf("Simulate: %v", err)
			}
			if result.Simulation.Status != "complete" || result.Simulation.Outcome != "continue" || len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].Outcome != tc.wantOutcome {
				t.Fatalf("simulation = %#v", result.Simulation)
			}
			policy := result.Simulation.Steps[0].CachePolicy
			if policy == nil || policy.RequestGate != tc.wantGate {
				t.Fatalf("cache policy = %#v, want request_gate=%q", policy, tc.wantGate)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if strings.Contains(string(encoded), "cache-secret-123") || strings.Contains(string(encoded), "sid=private") {
				t.Fatalf("cache trace leaked credentials: %s", encoded)
			}
		})
	}
}

func TestSimulateThrottleRuleReportsPolicyWithoutPredictingAdmission(t *testing.T) {
	rule := api.EdgeRuleResponse{
		ID: "route-throttle", Enabled: true, Kind: "throttle", MatchHost: "example.com", MatchPath: "/api/*", Priority: 5,
		Action: json.RawMessage(`{"throttle":{"requests_per_second":2.5,"burst":8,"key_by":"jwt_claim","jwt_claim_name":"org_id","max_keys_per_rule":250,"missing_key_policy":"reject"}}`),
	}
	input := edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/api/orders", Method: http.MethodPost,
		Headers: http.Header{"Authorization": []string{"Bearer secret-token"}}, AppMaintenanceLoaded: true,
		AppThrottleContextLoaded: true, AppRequestRateRPS: 4, AppRequestRateBurst: 12, AccountRequestRateRPM: 6000,
		ThrottlePlanLimitsLoaded: true, ThrottlePlanMaxRPS: 20, ThrottlePlanMaxBurst: 100,
	}
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Status != "incomplete" || result.Simulation.Outcome != "needs_throttle_runtime_context" || result.Simulation.StoppedAt != "throttle" {
		t.Fatalf("simulation = %#v; throttle state must not be guessed", result.Simulation)
	}
	if len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].Outcome != "throttle_policy_candidate" || result.Simulation.Steps[0].RuleID != "route-throttle" {
		t.Fatalf("throttle step = %#v", result.Simulation.Steps)
	}
	policy := result.Simulation.Steps[0].ThrottlePolicy
	if policy == nil || policy.RequestsPerSecond != 2.5 || policy.Burst != 8 || policy.KeyBy != api.ThrottleKeyByJWTClaim || policy.JWTClaimName != "org_id" || policy.MaxKeysPerRule != 250 || policy.MaxKeysSource != "rule" || policy.MissingKeyPolicy != api.ThrottleMissingKeyReject {
		t.Fatalf("route-throttle policy = %#v", policy)
	}
	if policy.PlanMaxRPS != 20 || policy.PlanMaxBurst != 100 || policy.PlanCeilingStatus != "within_plan" || policy.AppRequestRPS != 4 || policy.AppRequestBurst != 12 || policy.AccountRequestRPM != 6000 {
		t.Fatalf("outer throttle ceilings = %#v", policy)
	}
	if result.Rules[0].Outcome != "throttle_policy_candidate" || result.Rules[0].ActionPreview == nil || result.Rules[0].ActionPreview.ThrottlePolicy == nil {
		t.Fatalf("per-rule throttle preview = %#v", result.Rules[0])
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(encoded), "secret-token") || !strings.Contains(result.Simulation.Reason, "admission and HTTP 429 are not predicted") {
		t.Fatalf("throttle trace leaked a credential or overclaimed its result: %s", encoded)
	}
}

func TestSimulateThrottleRuleUsesGatewayDefaultsAndMarksUnknownPlan(t *testing.T) {
	rule := api.EdgeRuleResponse{
		ID: "default-throttle", Enabled: true, Kind: "throttle", MatchHost: "example.com", MatchPath: "*",
		Action: json.RawMessage(`{"throttle":{"requests_per_second":1,"burst":2}}`),
	}
	result, err := edgeruletrace.Simulate(edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet, AppMaintenanceLoaded: true,
	}, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	policy := result.Simulation.Steps[0].ThrottlePolicy
	if policy == nil || policy.KeyBy != api.ThrottleKeyByNone || policy.MissingKeyPolicy != api.ThrottleMissingKeyShared || policy.MaxKeysPerRule != api.ThrottleMaxKeysPerRuleDefault || policy.MaxKeysSource != "platform_default" || policy.PlanCeilingStatus != "unavailable" {
		t.Fatalf("gateway default policy = %#v", policy)
	}
}

func TestSimulateThrottleRuleShowsGatewayDefensiveClamps(t *testing.T) {
	rule := api.EdgeRuleResponse{
		ID: "clamped-throttle", Enabled: true, Kind: "throttle", MatchHost: "example.com", MatchPath: "*",
		Action: json.RawMessage(`{"throttle":{"requests_per_second":0.5,"burst":0,"max_keys_per_rule":50000,"missing_key_policy":"unknown"}}`),
	}
	result, err := edgeruletrace.Simulate(edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet, AppMaintenanceLoaded: true,
	}, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	policy := result.Simulation.Steps[0].ThrottlePolicy
	if policy == nil || policy.RequestsPerSecond != 0.5 || policy.Burst != 0 || policy.GatewayRateRPS != 1 || policy.GatewayBurst != 1 || policy.MaxKeysPerRule != api.ThrottleMaxKeysPerRuleDefault*10 || policy.MaxKeysSource != "platform_ceiling" || policy.MissingKeyPolicy != api.ThrottleMissingKeyShared {
		t.Fatalf("gateway defensive clamps = %#v", policy)
	}
	if !strings.Contains(result.Rules[0].OutcomeReason, "configured 0.5 requests/s, burst 0 (gateway effective 1 requests/s and burst 1)") {
		t.Fatalf("clamp explanation = %q", result.Rules[0].OutcomeReason)
	}
}

func TestSimulateRetryRuleReportsPolicyWithoutPredictingReplay(t *testing.T) {
	rule := api.EdgeRuleResponse{
		ID: "retry-rule", Enabled: true, Kind: "retry", MatchHost: "example.com", MatchPath: "/orders/*", Priority: 4,
		Action: json.RawMessage(`{"retry":{"max_attempts":3,"allow_non_idempotent":true,"min_remaining_ms":400,"backoff_ms":100,"budget_percent":20,"budget_min_retries":2}}`),
	}
	input := edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/orders/42", Method: http.MethodPost,
		Headers: http.Header{"Idempotency-Key": []string{"opaque-request-key"}}, AppMaintenanceLoaded: true,
	}
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Status != "incomplete" || result.Simulation.Outcome != "needs_retry_runtime_context" || result.Simulation.StoppedAt != "retry" {
		t.Fatalf("simulation = %#v; a configured retry must not imply a replay", result.Simulation)
	}
	if len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].Outcome != "retry_policy_candidate" || result.Simulation.Steps[0].RuleID != "retry-rule" {
		t.Fatalf("retry step = %#v", result.Simulation.Steps)
	}
	policy := result.Simulation.Steps[0].RetryPolicy
	if policy == nil || policy.MaxAttempts != 3 || policy.MaxReplays != 2 || policy.MaxAttemptsSource != "rule" || !policy.AllowNonIdempotent || policy.MethodEligibility != "non_idempotent_allowed_with_key" || !policy.IdempotencyKeyPresent {
		t.Fatalf("retry policy = %#v", policy)
	}
	if policy.MinRemainingMS != 400 || policy.BackoffMS != 100 || policy.BudgetPercent != 20 || policy.BudgetMinRetries != 2 {
		t.Fatalf("effective retry limits = %#v", policy)
	}
	if !strings.Contains(result.Simulation.Reason, "transport failure") || !strings.Contains(result.Simulation.Reason, "no replay or response outcome is predicted") {
		t.Fatalf("retry runtime caveat = %q", result.Simulation.Reason)
	}
	if result.Rules[0].Outcome != "retry_policy_candidate" || result.Rules[0].ActionPreview == nil || result.Rules[0].ActionPreview.RetryPolicy == nil {
		t.Fatalf("per-rule retry preview = %#v", result.Rules[0])
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(encoded), "opaque-request-key") {
		t.Fatalf("trace leaked the Idempotency-Key value: %s", encoded)
	}
}

func TestSimulateCircuitBreakerRuleReportsPolicyWithoutPredictingState(t *testing.T) {
	rule := api.EdgeRuleResponse{
		ID: "breaker-rule", Enabled: true, Kind: "circuit_breaker", MatchHost: "example.com", MatchPath: "/orders/*", Priority: 4,
		Action: json.RawMessage(`{"circuit_breaker":{"failure_threshold":0.25,"min_requests":20,"window_seconds":30,"open_seconds":10,"max_open_seconds":120}}`),
	}
	result, err := edgeruletrace.Simulate(edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/orders/42", Method: http.MethodPost, AppMaintenanceLoaded: true,
	}, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Status != "incomplete" || result.Simulation.Outcome != "needs_circuit_breaker_runtime_context" || result.Simulation.StoppedAt != "circuit_breaker" {
		t.Fatalf("simulation = %#v; a configured breaker must not imply a live state", result.Simulation)
	}
	if len(result.Simulation.Steps) != 1 || result.Simulation.Steps[0].Outcome != "circuit_breaker_policy_candidate" || result.Simulation.Steps[0].RuleID != "breaker-rule" {
		t.Fatalf("circuit-breaker step = %#v", result.Simulation.Steps)
	}
	policy := result.Simulation.Steps[0].CircuitBreakerPolicy
	if policy == nil || policy.FailureThreshold != 0.25 || policy.MinRequests != 20 || policy.WindowSeconds != 30 || policy.OpenSeconds != 10 || policy.MaxOpenSeconds != 120 {
		t.Fatalf("effective circuit-breaker policy = %#v", policy)
	}
	if !strings.Contains(result.Simulation.Reason, "operator feature gate") || !strings.Contains(result.Simulation.Reason, "open/half-open state") || !strings.Contains(result.Simulation.Reason, "target selection") {
		t.Fatalf("circuit-breaker runtime caveat = %q", result.Simulation.Reason)
	}
	if result.Rules[0].Outcome != "circuit_breaker_policy_candidate" || result.Rules[0].ActionPreview == nil || result.Rules[0].ActionPreview.CircuitBreakerPolicy == nil {
		t.Fatalf("per-rule circuit-breaker preview = %#v", result.Rules[0])
	}
	if !strings.Contains(result.Rules[0].OutcomeReason, "25% failure-ratio threshold") {
		t.Fatalf("threshold explanation = %q", result.Rules[0].OutcomeReason)
	}
	if !strings.Contains(result.Scope, "half-open probe outcomes are not predicted") {
		t.Fatalf("scope omits the circuit-breaker limitation: %q", result.Scope)
	}
}

func TestSimulateCircuitBreakerRuleUsesGatewayDefaultsAndCeilingFallback(t *testing.T) {
	rule := api.EdgeRuleResponse{
		ID: "breaker-defaults", Enabled: true, Kind: "circuit_breaker", MatchHost: "example.com", MatchPath: "*",
		Action: json.RawMessage(`{"circuit_breaker":{"failure_threshold":0,"min_requests":0,"window_seconds":0,"open_seconds":15,"max_open_seconds":0}}`),
	}
	result, err := edgeruletrace.Simulate(edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/orders", Method: http.MethodGet, AppMaintenanceLoaded: true,
	}, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	policy := result.Simulation.Steps[0].CircuitBreakerPolicy
	if policy == nil || policy.FailureThreshold != api.EdgeRuleCircuitDefaultFailureThreshold || policy.MinRequests != api.EdgeRuleCircuitDefaultMinRequests || policy.WindowSeconds != api.EdgeRuleCircuitDefaultWindowSeconds || policy.OpenSeconds != 15 || policy.MaxOpenSeconds != 15 {
		t.Fatalf("effective default/fallback policy = %#v", policy)
	}
	if !strings.Contains(result.Rules[0].OutcomeReason, "rolling 10-second window") || !strings.Contains(result.Rules[0].OutcomeReason, "backoff capped at 15 seconds") {
		t.Fatalf("effective policy explanation = %q", result.Rules[0].OutcomeReason)
	}
}

func TestSimulateRetryRuleUsesRuntimeDefaultsAndMethodGuard(t *testing.T) {
	rule := api.EdgeRuleResponse{
		ID: "retry-defaults", Enabled: true, Kind: "retry", MatchHost: "example.com", MatchPath: "*",
		Action: json.RawMessage(`{"retry":{"max_attempts":99,"allow_non_idempotent":false,"min_remaining_ms":0,"backoff_ms":9999,"budget_percent":0,"budget_min_retries":0}}`),
	}
	result, err := edgeruletrace.Simulate(edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/orders", Method: http.MethodPost, AppMaintenanceLoaded: true,
	}, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	policy := result.Simulation.Steps[0].RetryPolicy
	if policy == nil || policy.MaxAttempts != api.EdgeRuleRetryMaxAttempts || policy.MaxReplays != api.EdgeRuleRetryMaxAttempts-1 || policy.MaxAttemptsSource != "platform_ceiling" {
		t.Fatalf("attempt ceiling = %#v", policy)
	}
	if policy.MinRemainingMS != api.EdgeRuleRetryDefaultMinRemainingMs || policy.BackoffMS != 0 || policy.BudgetPercent != api.EdgeRuleRetryDefaultBudgetPercent || policy.BudgetMinRetries != api.EdgeRuleRetryDefaultBudgetMin {
		t.Fatalf("retry defaults = %#v", policy)
	}
	if policy.MethodEligibility != "non_idempotent_disabled" || policy.IdempotencyKeyPresent {
		t.Fatalf("method guard = %#v", policy)
	}
	if !strings.Contains(result.Simulation.Reason, "prevents replay") {
		t.Fatalf("blocked method explanation = %q", result.Simulation.Reason)
	}
}

func TestSimulateRetryRuleRequiresIdempotencyKeyForOptedInPost(t *testing.T) {
	rule := api.EdgeRuleResponse{
		ID: "retry-key-guard", Enabled: true, Kind: "retry", MatchHost: "example.com", MatchPath: "*",
		Action: json.RawMessage(`{"retry":{"max_attempts":2,"allow_non_idempotent":true}}`),
	}
	result, err := edgeruletrace.Simulate(edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/orders", Method: http.MethodPost, AppMaintenanceLoaded: true,
	}, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	policy := result.Simulation.Steps[0].RetryPolicy
	if policy == nil || !policy.AllowNonIdempotent || policy.IdempotencyKeyPresent || policy.MethodEligibility != "idempotency_key_required" {
		t.Fatalf("method guard = %#v", policy)
	}
	if !strings.Contains(result.Simulation.Reason, "idempotency_key_required") || !strings.Contains(result.Simulation.Reason, "prevents replay") {
		t.Fatalf("missing-key explanation = %q", result.Simulation.Reason)
	}
}

func TestSimulateRetryRuleRequiresEffectiveAttemptCount(t *testing.T) {
	rule := api.EdgeRuleResponse{
		ID: "invalid-retry", Enabled: true, Kind: "retry", MatchHost: "example.com", MatchPath: "*",
		Action: json.RawMessage(`{"retry":{"max_attempts":1}}`),
	}
	result, err := edgeruletrace.Simulate(edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/orders", Method: http.MethodGet, AppMaintenanceLoaded: true,
	}, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Rules[0].Outcome != "unavailable" || !strings.Contains(result.Rules[0].OutcomeReason, "gateway compilation would drop this rule") {
		t.Fatalf("invalid rule preview = %#v", result.Rules[0])
	}
	if result.Simulation.Status != "incomplete" || result.Simulation.Outcome != "unavailable" || result.Simulation.StoppedAt != "retry" {
		t.Fatalf("invalid rule simulation = %#v", result.Simulation)
	}
}

func TestSimulateBudgetRuleReportsHeaderOverrideAndPlanClamp(t *testing.T) {
	rule := budgetTraceRule(t, "budget-rule", api.EdgeRuleBudgetAction{
		BudgetMs: 4000, AllowOverrideHeader: "X-Tenant-Budget",
	})
	input := budgetTraceInput()
	input.RequestBudgetMaxMS = 5000
	input.Headers = http.Header{"X-Tenant-Budget": []string{"6500"}}
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Status != "complete" || result.Simulation.Outcome != "continue" || len(result.Simulation.Steps) != 1 {
		t.Fatalf("simulation = %#v", result.Simulation)
	}
	step := result.Simulation.Steps[0]
	if step.Phase != "budget" || step.Outcome != "budget_candidate" || step.RuleID != "budget-rule" {
		t.Fatalf("budget step = %#v", step)
	}
	policy := step.BudgetPolicy
	if policy == nil || policy.ConfiguredMS != 4000 || policy.BudgetMS != 5000 || policy.PlanMaxMS != 5000 || policy.Source != "ceiling_clamp" || policy.OverrideHeader != "X-Tenant-Budget" || policy.OverrideStatus != "applied_clamped" {
		t.Fatalf("effective budget policy = %#v", policy)
	}
	if result.Rules[0].ActionPreview == nil || result.Rules[0].ActionPreview.BudgetPolicy == nil || result.Rules[0].ActionPreview.BudgetPolicy.BudgetMS != 5000 {
		t.Fatalf("budget rule preview = %#v", result.Rules[0])
	}
	if !strings.Contains(step.Reason, "does not predict when it expires") {
		t.Fatalf("budget trace overclaims runtime behavior: %q", step.Reason)
	}
}

func TestSimulateBudgetRuleIgnoresInvalidOverride(t *testing.T) {
	rule := budgetTraceRule(t, "budget-rule", api.EdgeRuleBudgetAction{
		BudgetMs: 2400,
	})
	input := budgetTraceInput()
	input.Headers = http.Header{api.RequestBudgetDefaultOverrideHeader: []string{"3s"}}
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	policy := result.Simulation.Steps[0].BudgetPolicy
	if policy == nil || policy.BudgetMS != 2400 || policy.Source != "rule" || policy.OverrideStatus != "ignored_invalid" || policy.OverrideHeader != api.RequestBudgetDefaultOverrideHeader {
		t.Fatalf("invalid override policy = %#v", policy)
	}
}

func TestSimulateBudgetFallbackUsesAppTimeoutAndPlanCeiling(t *testing.T) {
	input := budgetTraceInput()
	input.RequestTimeoutS = 8
	input.RequestBudgetMaxMS = 5000
	result, err := edgeruletrace.Simulate(input, nil)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Status != "complete" || result.Simulation.Outcome != "continue" || len(result.Simulation.Steps) != 1 {
		t.Fatalf("simulation = %#v", result.Simulation)
	}
	policy := result.Simulation.Steps[0].BudgetPolicy
	if policy == nil || policy.ConfiguredMS != 8000 || policy.BudgetMS != 5000 || policy.PlanMaxMS != 5000 || policy.Source != "ceiling_clamp" || policy.OverrideStatus != "not_applicable" {
		t.Fatalf("fallback budget policy = %#v", policy)
	}

	planInput := budgetTraceInput()
	planInput.RequestTimeoutS = 0
	planInput.RequestBudgetMS = 2400
	planResult, err := edgeruletrace.Simulate(planInput, nil)
	if err != nil {
		t.Fatalf("Simulate plan fallback: %v", err)
	}
	planPolicy := planResult.Simulation.Steps[0].BudgetPolicy
	if planPolicy == nil || planPolicy.ConfiguredMS != 2400 || planPolicy.BudgetMS != 2400 || planPolicy.Source != "plan_default" {
		t.Fatalf("plan fallback budget policy = %#v", planPolicy)
	}
}

func TestSimulateBudgetRuleNeedsAppCeilingMetadata(t *testing.T) {
	rule := budgetTraceRule(t, "budget-rule", api.EdgeRuleBudgetAction{BudgetMs: 2400})
	input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/", Method: http.MethodPost, AppMaintenanceLoaded: true}
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Status != "incomplete" || result.Simulation.Outcome != "needs_app_request_budget" || result.Simulation.StoppedAt != "budget" {
		t.Fatalf("simulation without effective limits = %#v", result.Simulation)
	}
}

func TestSimulateRespondRulePrecedesCachePolicy(t *testing.T) {
	cacheRule := cacheTraceRule(t, "cache-rule", api.EdgeRuleCacheAction{MaxAgeSeconds: 60})
	respondRule := api.EdgeRuleResponse{
		ID: "respond-rule", Enabled: true, Kind: "respond", MatchHost: "*", MatchPath: "*", Priority: 1,
		Action: json.RawMessage(`{"respond":{"status_code":204}}`),
	}
	input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet, AppMaintenanceLoaded: true}
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{cacheRule, respondRule})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if result.Simulation.Outcome != "fixed_response" || result.Simulation.StoppedAt != "respond" || len(result.Simulation.Steps) != 1 {
		t.Fatalf("simulation = %#v", result.Simulation)
	}
}

func cacheTraceRule(t *testing.T, id string, action api.EdgeRuleCacheAction) api.EdgeRuleResponse {
	t.Helper()
	encodedAction, err := json.Marshal(map[string]any{"cache": action})
	if err != nil {
		t.Fatalf("Marshal cache action: %v", err)
	}
	return api.EdgeRuleResponse{
		ID: id, Enabled: true, Kind: "cache", MatchHost: "*", MatchPath: "*", Action: encodedAction,
	}
}

func budgetTraceRule(t *testing.T, id string, action api.EdgeRuleBudgetAction) api.EdgeRuleResponse {
	t.Helper()
	encodedAction, err := json.Marshal(map[string]any{"budget": action})
	if err != nil {
		t.Fatalf("Marshal budget action: %v", err)
	}
	return api.EdgeRuleResponse{
		ID: id, Enabled: true, Kind: "budget", MatchHost: "*", MatchPath: "*", Action: encodedAction,
	}
}

func budgetTraceInput() edgeruletrace.Input {
	return edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/payments", Method: http.MethodPost,
		AppMaintenanceLoaded: true, AppRequestBudgetLoaded: true,
		RequestBudgetMS: 3000, RequestBudgetMaxMS: 6000,
	}
}

func validateTraceInput(body string) edgeruletrace.Input {
	return edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/", Method: http.MethodPost,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    []byte(body), BodyProvided: true, RequestBodyMaxBytes: 1 << 20, AppMaintenanceLoaded: true,
	}
}

func validateTraceRule(t *testing.T, id, mode, schema string, contentTypes []string, maxBodyBytes int) api.EdgeRuleResponse {
	t.Helper()
	encodedAction, err := json.Marshal(map[string]any{"validate": api.EdgeRuleValidateAction{
		Schema: json.RawMessage(schema), ContentTypes: contentTypes, MaxBodyBytes: maxBodyBytes,
	}})
	if err != nil {
		t.Fatalf("Marshal validate action: %v", err)
	}
	return api.EdgeRuleResponse{
		ID: id, Enabled: true, Kind: "validate", MatchHost: "*", MatchPath: "*",
		ValidateMode: mode, Action: encodedAction,
	}
}

func limitTraceRule(t *testing.T, id string, buffered, streaming int) api.EdgeRuleResponse {
	t.Helper()
	encodedAction, err := json.Marshal(map[string]any{"limit": api.EdgeRuleLimitAction{
		MaxBodyBytes: buffered, MaxBodyBytesStreaming: streaming,
	}})
	if err != nil {
		t.Fatalf("Marshal limit action: %v", err)
	}
	return api.EdgeRuleResponse{
		ID: id, Enabled: true, Kind: "limit", MatchHost: "*", MatchPath: "*", Action: encodedAction,
	}
}

func corsTraceRule(t *testing.T, id string, matchMethods []string, action api.EdgeRuleCORSAction) api.EdgeRuleResponse {
	t.Helper()
	encodedAction, err := json.Marshal(map[string]any{"cors": action})
	if err != nil {
		t.Fatalf("Marshal CORS action: %v", err)
	}
	return api.EdgeRuleResponse{
		ID: id, Enabled: true, Kind: "cors", MatchHost: "*", MatchPath: "*",
		MatchMethods: matchMethods, Action: encodedAction,
	}
}
