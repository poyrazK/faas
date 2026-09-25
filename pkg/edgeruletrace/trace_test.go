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
