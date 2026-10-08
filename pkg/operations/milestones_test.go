package operations

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"testing"
	"time"
)

func milestoneSpec() api.OperationDefinitionSpec {
	spec := testSpec()
	spec.HTTPTransactionVersion = 1
	spec.Milestones = map[string]json.RawMessage{"paid": []byte(`{"type":"object","required":["total"],"properties":{"total":{"type":"integer"}},"additionalProperties":false}`)}
	return spec
}

func TestMilestoneContracts(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanPro).Operations
	spec := milestoneSpec()
	compiled, err := Compile(spec, limits)
	if err != nil {
		t.Fatal(err)
	}
	spec.Milestones["paid"][0] = '!'
	if compiled.Spec.Milestones["paid"][0] != '{' {
		t.Fatal("caller mutated pinned schema")
	}
	report := api.OperationMilestoneRequest{ID: uuid.NewString(), Name: "paid", Payload: []byte(`{"total":9.007199254740993e15}`), OccurredAt: time.Date(2026, 10, 7, 10, 0, 0, 123456789, time.FixedZone("zone", 3600))}
	canonical, err := compiled.CanonicalMilestone(report)
	if err != nil || canonical.OccurredAt.Location() != time.UTC || canonical.OccurredAt.Nanosecond() != 123456000 {
		t.Fatalf("canonical report: %+v %v", canonical, err)
	}
	equivalent := report
	equivalent.Payload = []byte(`{"total":9007199254740993}`)
	equivalent, err = compiled.CanonicalMilestone(equivalent)
	if err != nil || string(equivalent.Payload) != string(canonical.Payload) {
		t.Fatal("numeric replay identity changed")
	}
	for name, mutate := range map[string]func(*api.OperationMilestoneRequest){
		"undeclared":     func(r *api.OperationMilestoneRequest) { r.Name = "secret" },
		"schema":         func(r *api.OperationMilestoneRequest) { r.Payload = []byte(`{"total":"secret-value"}`) },
		"duplicate keys": func(r *api.OperationMilestoneRequest) { r.Payload = []byte(`{"total":1,"total":2}`) },
		"bound": func(r *api.OperationMilestoneRequest) {
			r.Payload = []byte(`"` + strings.Repeat("x", api.OperationMilestonePayloadMaxBytes) + `"`)
		},
		"zero time": func(r *api.OperationMilestoneRequest) { r.OccurredAt = time.Time{} },
		"UTC year underflow": func(r *api.OperationMilestoneRequest) {
			r.OccurredAt = time.Date(1, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 3600))
		},
		"UTC year overflow": func(r *api.OperationMilestoneRequest) {
			r.OccurredAt = time.Date(9999, 12, 31, 23, 59, 59, 0, time.FixedZone("west", -3600))
		},
		"zero UUID": func(r *api.OperationMilestoneRequest) { r.ID = uuid.Nil.String() },
	} {
		t.Run(name, func(t *testing.T) {
			bad := report
			mutate(&bad)
			_, err := compiled.CanonicalMilestone(bad)
			if err == nil || strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("unsafe acceptance/error: %v", err)
			}
		})
	}
	for name, mutate := range map[string]func(*api.OperationDefinitionSpec){
		"negotiation": func(s *api.OperationDefinitionSpec) { s.HTTPTransactionVersion = 0 },
		"name":        func(s *api.OperationDefinitionSpec) { s.Milestones["Upper"] = []byte(`true`) },
		"count": func(s *api.OperationDefinitionSpec) {
			for i := 0; i < api.OperationMilestoneDeclarationsMax; i++ {
				s.Milestones["n"+strings.Repeat("a", i)] = []byte(`true`)
			}
		},
		"schema bytes": func(s *api.OperationDefinitionSpec) {
			s.Milestones["paid"] = []byte(`{"description":"` + strings.Repeat("x", api.OperationMilestoneSchemasMaxBytes) + `"}`)
		},
		"remote schema": func(s *api.OperationDefinitionSpec) {
			s.Milestones["paid"] = []byte(`{"$ref":"https://example.com/schema"}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := milestoneSpec()
			mutate(&bad)
			if _, err := Compile(bad, limits); err == nil {
				t.Fatal("accepted invalid declaration")
			}
		})
	}
	next := milestoneSpec()
	next.Milestones["paid"] = []byte(`true`)
	changed, err := Compile(next, limits)
	if err != nil || changed.Revision == compiled.Revision {
		t.Fatal("milestone schema did not pin revision")
	}
	plain := testSpec()
	before, _ := Compile(plain, limits)
	plain.Milestones = map[string]json.RawMessage{}
	after, _ := Compile(plain, limits)
	if before.Revision != after.Revision {
		t.Fatal("empty declarations changed existing revision")
	}
}

func TestOperationWorkflowStepsAreBoundedPinnedAndCanonical(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanPro).Operations
	plain, err := Compile(milestoneSpec(), limits)
	if err != nil {
		t.Fatal(err)
	}
	spec := milestoneSpec()
	spec.WorkflowSteps = []api.OperationWorkflowSpec{
		{Workflow: "order-lifecycle", Title: "Order lifecycle", Step: "payment", Label: "Payment authorized", Milestone: "paid", InstanceIDFrom: "/workflow_run_id", Position: 2},
		{Workflow: "order-lifecycle", Title: "Order lifecycle", Step: "placed", Label: "Order placed", Milestone: "paid", InstanceIDFrom: "/workflow_run_id", Position: 1},
	}
	if _, err := Compile(spec, limits); err == nil {
		t.Fatal("the same milestone mapped to two steps in one workflow")
	}
	spec.WorkflowSteps = []api.OperationWorkflowSpec{
		{Workflow: "order-lifecycle", Title: "Order lifecycle", Step: "payment", Label: "Payment authorized", Milestone: "paid", InstanceIDFrom: "/workflow_run_id", Position: 2},
	}
	compiled, err := Compile(spec, limits)
	if err != nil || compiled.Revision == plain.Revision {
		t.Fatalf("workflow metadata was not pinned into the definition: %+v %v", compiled, err)
	}
	for name, mutate := range map[string]func(*api.OperationDefinitionSpec){
		"undeclared milestone": func(s *api.OperationDefinitionSpec) { s.WorkflowSteps[0].Milestone = "unknown" },
		"missing transaction":  func(s *api.OperationDefinitionSpec) { s.HTTPTransactionVersion = 0 },
		"position bound":       func(s *api.OperationDefinitionSpec) { s.WorkflowSteps[0].Position = api.OperationWorkflowStepsMax + 1 },
		"control label":        func(s *api.OperationDefinitionSpec) { s.WorkflowSteps[0].Label = "Payment\nconfirmed" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := spec
			bad.WorkflowSteps = append([]api.OperationWorkflowSpec(nil), spec.WorkflowSteps...)
			mutate(&bad)
			if _, err := Compile(bad, limits); err == nil {
				t.Fatal("accepted invalid workflow projection")
			}
		})
	}
}
