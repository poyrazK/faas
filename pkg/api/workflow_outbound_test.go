package api

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"testing"
)

func TestWorkflowOutboundValidationAndWire(t *testing.T) {
	base := WorkflowStepSpec{Name: "crm", Outbound: &WorkflowOutboundSpec{IntegrationID: uuid.NewString(), Method: "POST", Path: "/v1/contacts", IdempotencySupported: true}, Input: json.RawMessage(`{"email":"{{input.email}}"}`), Retry: &WorkflowRetrySpec{MaxAttempts: 3, Backoff: "exponential"}}
	tests := []struct {
		name   string
		mutate func(*WorkflowStepSpec)
		valid  bool
	}{
		{"valid", func(*WorkflowStepSpec) {}, true},
		{"unsafe retries", func(s *WorkflowStepSpec) { s.Outbound.IdempotencySupported = false }, false},
		{"unsafe single attempt", func(s *WorkflowStepSpec) { s.Outbound.IdempotencySupported = false; s.Retry = nil }, true},
		{"absolute URL", func(s *WorkflowStepSpec) { s.Outbound.Path = "https://example.com" }, false},
		{"encoded route", func(s *WorkflowStepSpec) { s.Outbound.Path = "/v1/%2e%2e/admin" }, false},
		{"query", func(s *WorkflowStepSpec) { s.Outbound.Path = "/v1/contacts?a=1" }, false},
		{"foreign target", func(s *WorkflowStepSpec) { s.Run = "other" }, false},
		{"outer method", func(s *WorkflowStepSpec) { s.Method = "POST" }, false},
		{"invalid integration", func(s *WorkflowStepSpec) { s.Outbound.IntegrationID = "missing" }, false},
		{"get with body", func(s *WorkflowStepSpec) { s.Outbound.Method = "GET" }, false},
		{"get without body", func(s *WorkflowStepSpec) { s.Outbound.Method = "GET"; s.Input = nil }, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			step := base
			target := *base.Outbound
			step.Outbound = &target
			test.mutate(&step)
			_, err := ValidateWorkflowDAG(WorkflowSpec{Name: "sync", Steps: []WorkflowStepSpec{step}}, PlanHobby)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
		})
	}
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			var raw []byte
			var err error
			var decoded WorkflowStepSpec
			if format == "json" {
				raw, err = json.Marshal(base)
				if err == nil {
					err = json.Unmarshal(raw, &decoded)
				}
			} else {
				raw = []byte(fmt.Sprintf("name: crm\ninput: {email: \"{{input.email}}\"}\noutbound: {integration_id: %s, method: POST, path: /v1/contacts, idempotency_supported: true}\nretry: {max_attempts: 3, backoff: exponential}\n", base.Outbound.IntegrationID))
				if err == nil {
					err = yaml.Unmarshal(raw, &decoded)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Outbound == nil || *decoded.Outbound != *base.Outbound || string(decoded.Input) != string(base.Input) {
				t.Fatalf("roundtrip=%s %#v", raw, decoded)
			}
		})
	}
	if err := json.Unmarshal([]byte(`{"name":"bad","outbound":{"integration_id":"x","method":"GET","path":"/","authorization":"secret"}}`), new(WorkflowStepSpec)); err == nil {
		t.Fatal("accepted unknown credential field")
	}
}
