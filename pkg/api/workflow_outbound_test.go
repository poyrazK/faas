package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"net/url"
	"reflect"
	"testing"
)

func TestWorkflowOutboundValidationAndWire(t *testing.T) {
	base := WorkflowStepSpec{Name: "crm", Outbound: &WorkflowOutboundSpec{IntegrationID: uuid.NewString(), Method: "POST", Path: "/v1/contacts/{{input.id}}", Query: map[string]string{"source": "automation"}, IdempotencySupported: true}, Input: json.RawMessage(`{"email":"{{input.email}}"}`), Retry: &WorkflowRetrySpec{MaxAttempts: 3, Backoff: "exponential"}}
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
		{"partial dynamic segment", func(s *WorkflowStepSpec) { s.Outbound.Path = "/v1/contacts/id-{{input.id}}" }, false},
		{"unsafe query key", func(s *WorkflowStepSpec) { s.Outbound.Query = map[string]string{"a&b": "value"} }, false},
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
				raw = []byte(fmt.Sprintf("name: crm\ninput: {email: \"{{input.email}}\"}\noutbound: {integration_id: %s, method: POST, path: \"/v1/contacts/{{input.id}}\", query: {source: automation}, idempotency_supported: true}\nretry: {max_attempts: 3, backoff: exponential}\n", base.Outbound.IntegrationID))
				if err == nil {
					err = yaml.Unmarshal(raw, &decoded)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Outbound == nil || !reflect.DeepEqual(decoded.Outbound, base.Outbound) || string(decoded.Input) != string(base.Input) {
				t.Fatalf("roundtrip=%s %#v", raw, decoded)
			}
		})
	}
	if err := json.Unmarshal([]byte(`{"name":"bad","outbound":{"integration_id":"x","method":"GET","path":"/","authorization":"secret"}}`), new(WorkflowStepSpec)); err == nil {
		t.Fatal("accepted unknown credential field")
	}
}

func TestWorkflowOutboundTargetResolutionAndAuthorizationMatching(t *testing.T) {
	spec := WorkflowOutboundSpec{
		IntegrationID: uuid.NewString(), Method: "GET",
		Path:  "/v1/contacts/{{input.id}}",
		Query: map[string]string{"email": "{{input.email}}", "page": "{{input.page}}", "q": "prefix-{{input.term}}"},
	}
	input := json.RawMessage(`{"id":"contact 42","email":"a+b@example.com","page":3,"term":"café & co"}`)
	resolved, err := ResolveWorkflowOutboundTarget(spec, input, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantQuery := url.Values{"email": {"a+b@example.com"}, "page": {"3"}, "q": {"prefix-café & co"}}.Encode()
	if resolved.Path != "/v1/contacts/contact%2042" || resolved.RawQuery != wantQuery || resolved.PathTemplate != spec.Path || !reflect.DeepEqual(resolved.QueryTemplate, spec.Query) {
		t.Fatalf("resolved outbound target = %+v, raw query %q; want %q", resolved, resolved.RawQuery, wantQuery)
	}
	if !WorkflowOutboundRequestMatchesTemplate(resolved.PathTemplate, resolved.Path, resolved.QueryTemplate, resolved.RawQuery) {
		t.Fatal("resolved path and query did not match their definition templates")
	}
	if WorkflowOutboundRequestMatchesTemplate(resolved.PathTemplate, "/v1/admin", resolved.QueryTemplate, resolved.RawQuery) {
		t.Fatal("accepted a path outside the definition template")
	}
	if WorkflowOutboundRequestMatchesTemplate(resolved.PathTemplate, resolved.Path, resolved.QueryTemplate, "email=a%2bb%40example.com&page=3&q=prefix-caf%C3%A9+%26+co") {
		t.Fatal("accepted a noncanonical or differently materialized query")
	}
	for _, id := range []string{"", ".", "..", "a/b", `a\\b`, "a%b"} {
		bad := spec
		bad.Path = "/v1/contacts/{{input.id}}"
		input, _ := json.Marshal(map[string]string{"id": id})
		if _, err := ResolveWorkflowOutboundTarget(bad, input, nil, nil); err == nil {
			t.Errorf("accepted unsafe path segment value %q", id)
		}
	}
}

func TestWorkflowOutboundTemplatesRequireDeclaredDependencies(t *testing.T) {
	spec := WorkflowSpec{Name: "crm", Steps: []WorkflowStepSpec{
		{Name: "lookup", Run: "lookup"},
		{Name: "update", Outbound: &WorkflowOutboundSpec{IntegrationID: uuid.NewString(), Method: "PATCH", Path: "/v1/contacts/{{steps.lookup.output.id}}", Query: map[string]string{"source": "{{steps.lookup.output.source}}"}, IdempotencySupported: true}},
	}}
	if _, err := ValidateWorkflowDAG(spec, PlanHobby); !errors.Is(err, ErrWorkflowInputOutputDependency) {
		t.Fatalf("outbound template without dependency accepted: %v", err)
	}
	spec.Steps[1].DependsOn = []string{"lookup"}
	if _, err := ValidateWorkflowDAG(spec, PlanHobby); err != nil {
		t.Fatalf("outbound template with dependency rejected: %v", err)
	}
}
