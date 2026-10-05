package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAutomationSimulationAPIHasNoExecutionEffects(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "simulation")
	ctx := context.Background()
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(ctx, e.acct.ID, hash, "reader", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	// No deployment, disabled runtime and no draft: simulation is sample-only.
	e.s.WithWorkflowRuntimeEnabled(false)
	before, _ := e.store.ListEvents(ctx, e.acct.ID, 100)
	spec := api.WorkflowSpec{Name: "paid", Steps: []api.WorkflowStepSpec{
		{Name: "lookup", Run: "lookup", Input: json.RawMessage(`"{{input.id}}"`)},
		{Name: "send", Path: "/send", DependsOn: []string{"lookup"}, Input: json.RawMessage(`"{{steps.lookup.output}}"`)},
	}}
	request := api.SimulateAutomationRequest{Definition: spec, Input: json.RawMessage(`{"id":9007199254740993}`), MockOutputs: map[string]json.RawMessage{"lookup": json.RawMessage(`null`)}}
	response := e.do(t, "POST", "/v1/apps/"+app.Slug+"/automations:simulate", request, nil)
	var trace api.SimulateAutomationResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &trace) != nil || !trace.DefinitionValid || trace.Complete || len(trace.Trace) != 2 {
		t.Fatalf("simulation=%d %s", response.Code, response.Body.String())
	}
	if trace.Trace[0].State != "mocked" || trace.Trace[1].State != "would_execute" || string(trace.Trace[1].Input) != "null" {
		t.Fatalf("sample flow: %+v", trace)
	}
	runs, _, err := e.store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{Limit: 10})
	if err != nil || len(runs) != 0 {
		t.Fatalf("created run: %v %v", runs, err)
	}
	invocations, err := e.store.ListInvocationsForApp(ctx, app.ID)
	if err != nil || len(invocations) != 0 {
		t.Fatalf("invoked app: %v %v", invocations, err)
	}
	drafts, err := e.store.ListAutomations(ctx, app.ID)
	if err != nil || len(drafts) != 0 {
		t.Fatalf("saved draft: %v %v", drafts, err)
	}
	after, err := e.store.ListEvents(ctx, e.acct.ID, 100)
	if err != nil || len(after) != len(before) {
		t.Fatalf("published/audited sample data: %v %v", after, err)
	}
}

func TestAutomationSimulationAPIBoundsOwnershipAndValidation(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "simulation-bounds")
	base := "/v1/apps/" + app.Slug + "/automations:simulate"
	spec := api.WorkflowSpec{Name: "draft", Steps: []api.WorkflowStepSpec{{Name: "a", Run: "a"}}}
	for _, tc := range []struct {
		name   string
		body   any
		status int
	}{
		{"unknown request field", map[string]any{"definition": spec, "extra": true}, 400},
		{"unknown definition field", map[string]any{"definition": map[string]any{"name": "draft", "steps": spec.Steps, "extra": true}}, 400},
		{"unknown mock", api.SimulateAutomationRequest{Definition: spec, MockOutputs: map[string]json.RawMessage{"missing": json.RawMessage(`null`)}}, 400},
		{"oversized input", api.SimulateAutomationRequest{Definition: spec, Input: json.RawMessage(`"` + strings.Repeat("x", int(api.WorkflowRunInputMaxBytes)) + `"`)}, 413},
		{"oversized body", map[string]any{"definition": spec, "input": strings.Repeat("x", int(api.AutomationSimulationRequestMaxBytes))}, 413},
		{"missing definition", map[string]any{}, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.do(t, "POST", base, tc.body, nil)
			if rec.Code != tc.status {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if rec.Code == 413 && (!strings.Contains(rec.Body.String(), "request_too_large") || !strings.Contains(rec.Body.String(), "limit")) {
				t.Fatalf("missing limit problem: %s", rec.Body.String())
			}
		})
	}
	other, err := e.store.CreateAccount(context.Background(), "other-simulation@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := e.store.CreateApp(context.Background(), state.App{AccountID: other.ID, Slug: "other-simulation", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, "POST", "/v1/apps/"+otherApp.Slug+"/automations:simulate", api.SimulateAutomationRequest{Definition: spec}, nil); rec.Code != 404 {
		t.Fatalf("ownership: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "POST", base, api.SimulateAutomationRequest{Definition: spec}, map[string]string{"Authorization": ""}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("auth: %d %s", rec.Code, rec.Body.String())
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "unrelated", []string{api.ScopeRunsWrite}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	if rec := e.do(t, "POST", base, api.SimulateAutomationRequest{Definition: spec}, nil); rec.Code != 403 {
		t.Fatalf("scope: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAutomationSimulationChecksOutboundBindingsWithoutCalling(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "simulate-outbound")
	ctx := context.Background()
	offer := state.OutboundIntegrationOffer{ID: uuid.NewString(), AccountID: app.AccountID, Name: "crm", Origin: "https://api.example.com", AllowedMethods: []string{"POST"}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true, OwnerKind: "customer", CredentialSource: "customer_sealed", RequestPolicy: api.DefaultOutboundRequestPolicy()}
	if _, err := e.store.CreateOutboundIntegration(ctx, offer); err != nil {
		t.Fatal(err)
	}
	request := api.SimulateAutomationRequest{Definition: api.WorkflowSpec{Name: "crm", Steps: []api.WorkflowStepSpec{{Name: "send", Outbound: &api.WorkflowOutboundSpec{IntegrationID: offer.ID, Method: "POST", Path: "/v1/contacts"}}}}, MockOutputs: map[string]json.RawMessage{"send": json.RawMessage(`{"status_code":200,"body":null}`)}}
	base := "/v1/apps/" + app.Slug + "/automations:simulate"
	response := e.do(t, "POST", base, request, nil)
	var got api.SimulateAutomationResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &got) != nil || got.DefinitionValid || len(got.Trace) != 0 || got.Complete {
		t.Fatalf("unbound: %d %s", response.Code, response.Body.String())
	}
	if err := e.store.SetOutboundCredential(ctx, app.AccountID, offer.ID, []byte("sealed-not-opened")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.BindOutboundIntegration(ctx, app.AccountID, app.ID, offer.ID); err != nil {
		t.Fatal(err)
	}
	response = e.do(t, "POST", base, request, nil)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &got) != nil || !got.DefinitionValid || !got.Complete || len(got.Trace) != 1 || got.Trace[0].IntegrationID != offer.ID || strings.Contains(response.Body.String(), "sealed-not-opened") || strings.Contains(response.Body.String(), offer.Origin) {
		t.Fatalf("bound simulation: %d %s", response.Code, response.Body.String())
	}
}
