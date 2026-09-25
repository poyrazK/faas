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
