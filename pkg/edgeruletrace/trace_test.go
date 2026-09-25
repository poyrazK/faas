package edgeruletrace_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

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

func TestSimulateInlineCORSRuleAppliesResponseHeaders(t *testing.T) {
	rule := corsTraceRule(t, "cors-rule", []string{"GET"}, api.EdgeRuleCORSAction{
		AllowOrigins: []string{"https://app.example.com"}, AllowMethods: []string{"GET", "POST"},
		AllowHeaders: []string{"Content-Type", "X-Request-ID"}, ExposeHeaders: []string{"X-Request-ID"},
		AllowCredentials: true, MaxAgeSeconds: 600,
	})
	input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet, Headers: http.Header{"Origin": []string{"https://app.example.com"}}}
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
		Headers: http.Header{"Origin": []string{"https://app.example.com"}},
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
	}}
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
			result, err := edgeruletrace.Simulate(edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/", Method: tc.method, Headers: headers}, []api.EdgeRuleResponse{rule})
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
	result, err := edgeruletrace.Simulate(edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/", Method: http.MethodOptions}, []api.EdgeRuleResponse{rule})
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
	result, err := edgeruletrace.Simulate(edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet}, []api.EdgeRuleResponse{rule})
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
		Headers: http.Header{"Origin": []string{"https://app.example.com"}},
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
		Headers: http.Header{"Origin": []string{"https://app.example.com"}},
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
	input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/", Method: http.MethodGet, CorsPresets: []api.CorsPresetResponse{{
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

func validateTraceInput(body string) edgeruletrace.Input {
	return edgeruletrace.Input{
		App: "demo", Host: "example.com", Path: "/", Method: http.MethodPost,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    []byte(body), BodyProvided: true, RequestBodyMaxBytes: 1 << 20,
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
