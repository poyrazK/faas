// adr: 640
package main

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOperationMilestoneQueryBoundaries(t *testing.T) {
	for _, tc := range []struct {
		query                      string
		operator, reference, valid bool
	}{
		{"limit=1&cursor=abc", true, false, true},
		{"scope=default&subject_type=order&subject_id=42", true, true, true},
		{"app_id=app&scope=default&subject_type=order&subject_id=42", false, true, true},
		{"scope=default&subject_type=order", true, true, false},
		{"scope=default&subject_type=order&subject_id=42&tenant_id=bob", false, true, false},
		{"scope=default&subject_type=order&subject_id=42&app_id=other", true, true, false},
		{"limit=1&limit=2", true, false, false},
		{"subject_type=order&subject_id=42", true, false, false},
		{"owner=bob", false, false, false},
	} {
		_, err := operationMilestoneHistoryOptions(httptest.NewRequest("GET", "/?"+tc.query, nil), tc.operator, tc.reference, "app")
		if (err == nil) != tc.valid {
			t.Fatalf("query %s: %v", tc.query, err)
		}
	}
}

func TestDashboardOperationMilestones(t *testing.T) {
	f := newDashboardOperationFixture(t)
	declarations := []struct {
		name, path, milestone, step, label string
		position                           int
	}{
		{"place-order", "/orders", "order-placed", "placed", "Order placed", 1},
		{"authorize-payment", "/payments/authorize", "payment-authorized", "paid", "Payment authorized", 2},
		{"fulfill-order", "/orders/fulfill", "order-fulfilled", "fulfilled", "Order fulfilled", 3},
	}
	var operations []state.Operation
	for _, declaration := range declarations {
		spec := f.def.Spec
		spec.Name, spec.Path = declaration.name, declaration.path
		spec.HTTPTransactionVersion = 1
		spec.Milestones = map[string]json.RawMessage{declaration.milestone: []byte(`true`)}
		spec.WorkflowSteps = []api.OperationWorkflowSpec{{Workflow: "order-lifecycle", Title: "Order lifecycle", Step: declaration.step,
			Label: declaration.label, Milestone: declaration.milestone, InstanceIDFrom: "/workflow_run_id", Position: declaration.position}}
		spec.Subject = &api.OperationSubjectSpec{Type: "order", IDFrom: "/order_id"}
		def, err := f.store.PutOperationDefinition(t.Context(), state.OperationDefinition{AccountID: f.account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: f.app.ID, Scope: f.def.Scope, DeploymentID: f.def.DeploymentID, Spec: spec}})
		if err != nil {
			t.Fatal(err)
		}
		op, _, err := f.store.AdmitOperation(t.Context(), state.OperationAdmission{AccountID: f.account.ID, DefinitionID: def.ID, PlatformTenantID: f.tenant.ID, IdempotencyKey: "facts-" + declaration.step, Input: []byte(`{"order_id":"order-42"}`)})
		if err != nil {
			t.Fatal(err)
		}
		operations = append(operations, op)
	}
	node, err := f.store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := f.store.CreateInstance(t.Context(), f.app.ID, f.def.DeploymentID, "stopped", 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	dangerous := "</code><script>alert(42)</script>"
	reports := make([]api.OperationMilestone, 0, len(declarations))
	for i, declaration := range declarations {
		op := operations[i]
		inv, err := f.store.ClaimInvocationWithCap(t.Context(), op.CurrentInvocationID, instance.ID, 60, 100)
		if err != nil {
			t.Fatal(err)
		}
		var headers map[string]string
		_ = json.Unmarshal(inv.Headers, &headers)
		authority := state.OperationExecutionAuthority{AccountID: f.account.ID, AppID: f.app.ID, InstanceID: instance.ID, InvocationID: inv.ID, Attempt: inv.Attempts, Capability: headers[api.OperationCapabilityHeader]}
		payload, _ := json.Marshal(map[string]string{"public_note": dangerous, "workflow_run_id": "run-1"})
		report := api.OperationMilestoneRequest{ID: uuid.NewString(), Name: declaration.milestone, Payload: payload, OccurredAt: time.Now().Add(-time.Hour)}
		fact, err := f.store.ReportOperationMilestone(t.Context(), op.ID, authority, report)
		if err != nil {
			t.Fatal(err)
		}
		if len(fact.WorkflowSteps) != 1 || fact.WorkflowSteps[0].Position != declaration.position {
			t.Fatalf("milestone did not retain its pinned workflow mapping: %+v", fact)
		}
		reports = append(reports, fact)
	}
	base := dashboardCustomerOperationsURL(f.app.Slug)
	query := url.Values{"scope": {f.def.Scope}, "subject_type": {"order"}, "subject_id": {"order-42"}}
	for _, path := range []string{base + "/" + operations[0].ID, base + "?" + query.Encode()} {
		body := f.get(t, path, http.StatusOK).Body.String()
		if !strings.Contains(body, "Business milestones") || !strings.Contains(body, "Observed business workflows") || strings.Contains(body, dangerous) || !strings.Contains(body, "public_note") {
			t.Fatalf("missing or unsafe fact projection: %s", body)
		}
	}
	workflowBody := f.get(t, base+"?"+query.Encode(), http.StatusOK).Body.String()
	for i, declaration := range declarations {
		if strings.Count(workflowBody, declaration.label) != 1 || strings.Count(workflowBody, reports[i].ID) != 1 {
			t.Fatalf("observed workflow repeated or omitted a fact for %s: %s", declaration.step, workflowBody)
		}
	}
	// A reference selection cannot see facts from another environment or entity.
	query.Set("scope", "staging")
	if body := f.get(t, base+"?"+query.Encode(), http.StatusOK).Body.String(); strings.Contains(body, reports[0].ID) {
		t.Fatal("timeline crossed environment")
	}
	query.Set("scope", f.def.Scope)
	query.Set("subject_id", "other")
	if body := f.get(t, base+"?"+query.Encode(), http.StatusOK).Body.String(); strings.Contains(body, reports[0].ID) {
		t.Fatal("timeline crossed entity")
	}
}
