package main

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAutomationOutboundValidationAndPublication(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "outbound-authoring")
	ctx := context.Background()
	if _, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:x", Workflows: json.RawMessage(`[]`)}); err != nil {
		t.Fatal(err)
	}
	offer := state.OutboundIntegrationOffer{ID: uuid.NewString(), AccountID: app.AccountID, Name: "crm", Origin: "https://api.example.com", AllowedMethods: []string{"POST"}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true, OwnerKind: "customer", CredentialSource: "customer_sealed", RequestPolicy: api.DefaultOutboundRequestPolicy()}
	if _, err := e.store.CreateOutboundIntegration(ctx, offer); err != nil {
		t.Fatal(err)
	}
	spec := api.WorkflowSpec{Name: "crm", Steps: []api.WorkflowStepSpec{{Name: "send", Outbound: &api.WorkflowOutboundSpec{IntegrationID: offer.ID, Method: "POST", Path: "/v1/contacts"}, Input: json.RawMessage(`{"email":"{{input.email}}"}`)}}}
	base := "/v1/apps/" + app.Slug + "/automations"
	response := e.do(t, "POST", base+":validate", api.ValidateAutomationRequest{Definition: spec}, nil)
	var validation api.ValidateAutomationResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &validation) != nil || validation.Valid || len(validation.Issues) == 0 {
		t.Fatalf("accepted an unbound credential: %d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "PUT", base+"/crm", api.SaveAutomationDraftRequest{Definition: spec}, nil)
	var draft api.AutomationResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &draft) != nil || draft.Draft.Steps[0].Outbound == nil {
		t.Fatalf("outbound draft lost: %d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "POST", base+"/crm/publish", api.PublishAutomationRequest{ExpectedVersion: draft.Version}, nil)
	if response.Code != 422 {
		t.Fatalf("published without binding: %d %s", response.Code, response.Body.String())
	}
	if err := e.store.SetOutboundCredential(ctx, app.AccountID, offer.ID, []byte("sealed-placeholder")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.BindOutboundIntegration(ctx, app.AccountID, app.ID, offer.ID); err != nil {
		t.Fatal(err)
	}
	response = e.do(t, "POST", base+":validate", api.ValidateAutomationRequest{Definition: spec}, nil)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &validation) != nil || !validation.Valid {
		t.Fatalf("bound validation: %d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "POST", base+"/crm/publish", api.PublishAutomationRequest{ExpectedVersion: draft.Version}, nil)
	if response.Code != 200 {
		t.Fatalf("bound publication: %d %s", response.Code, response.Body.String())
	}
}

func TestAutomationAPI(t *testing.T) {
	e := setup(t, api.PlanHobby)
	e.s.WithWorkflowRuntimeEnabled(true)
	app := seedWorkflowApp(t, e, "authoring-api")
	_, err := e.store.CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:x", Workflows: json.RawMessage(`[]`)})
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/" + app.Slug + "/automations"
	spec := api.WorkflowSpec{Name: "invoice", Trigger: &api.WorkflowTriggerSpec{Type: "event", Source: "billing", EventType: "invoice.paid"}, Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/invoice", When: &api.WorkflowGuardSpec{Ref: "input.invoice_id", Op: "exists", Value: json.RawMessage("true")}}}}
	response := e.do(t, "POST", base+":validate", api.ValidateAutomationRequest{Definition: spec}, nil)
	var valid api.ValidateAutomationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &valid) != nil || !valid.Valid {
		t.Fatalf("validate=%d %s", response.Code, response.Body.String())
	}
	rows, _ := e.store.ListAutomations(context.Background(), app.ID)
	if len(rows) != 0 {
		t.Fatal("validation persisted a draft")
	}
	response = e.do(t, "PUT", base+"/invoice", api.SaveAutomationDraftRequest{Definition: spec}, nil)
	var draft api.AutomationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &draft) != nil || draft.Version == 0 || draft.Published != nil {
		t.Fatalf("save=%d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "PUT", base+"/invoice", api.SaveAutomationDraftRequest{Definition: spec}, nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("CAS=%d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "POST", base+"/invoice/publish", api.PublishAutomationRequest{ExpectedVersion: draft.Version}, map[string]string{"Idempotency-Key": "publish-invoice"})
	var published api.AutomationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &published) != nil || published.PublishedVersion == 0 || published.Source != "dashboard" {
		t.Fatalf("publish=%d %s", response.Code, response.Body.String())
	}
	if published.Published == nil || published.Published.Steps[0].When == nil || published.Published.Steps[0].When.Ref != "input.invoice_id" {
		t.Fatal("publication lost guard")
	}
	replay := e.do(t, "POST", base+"/invoice/publish", api.PublishAutomationRequest{ExpectedVersion: draft.Version}, map[string]string{"Idempotency-Key": "publish-invoice"})
	if replay.Code != http.StatusOK || replay.Body.String() != response.Body.String() {
		t.Fatalf("publish replay=%d %s", replay.Code, replay.Body.String())
	}
	paused := false
	response = e.do(t, "PUT", base+"/invoice/enabled", api.SetAutomationEnabledRequest{ExpectedVersion: published.Version, Enabled: &paused}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("pause=%d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "PUT", base+"/invoice/enabled", map[string]any{"expected_version": published.Version}, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled=%d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "POST", "/v1/apps/"+app.Slug+"/workflows/invoice/runs", map[string]any{"invoice_id": "123"}, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("run=%d %s", response.Code, response.Body.String())
	}
	runs, _, _ := e.store.ListWorkflowRuns(context.Background(), app.ID, state.ListWorkflowRunsOpts{Limit: 10})
	if len(runs) != 1 {
		t.Fatal("missing published run")
	}
	var snapshot api.WorkflowSpec
	_ = json.Unmarshal(runs[0].DefinitionSnapshot, &snapshot)
	if snapshot.Steps[0].Path != "/invoice" {
		t.Fatalf("snapshot=%s", runs[0].DefinitionSnapshot)
	}
	// Authenticated customers cannot inspect or mutate a different account's app.
	other, _ := e.store.CreateAccount(context.Background(), "other-author@example.com", api.PlanHobby)
	otherApp, _ := e.store.CreateApp(context.Background(), state.App{AccountID: other.ID, Slug: "other-author", Type: state.AppTypeApp})
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		path := "/v1/apps/" + otherApp.Slug + "/automations/invoice"
		if method == "DELETE" {
			path += "?expected_version=1"
		}
		response = e.do(t, method, path, api.SaveAutomationDraftRequest{Definition: spec}, nil)
		if response.Code != http.StatusNotFound {
			t.Fatalf("isolation %s=%d %s", method, response.Code, response.Body.String())
		}
	}
}
func TestAutomationAPIBoundsAndPlan(t *testing.T) {
	e := setup(t, api.PlanFree)
	app := seedWorkflowApp(t, e, "free-authoring")
	base := "/v1/apps/" + app.Slug + "/automations"
	spec := api.WorkflowSpec{Name: "test", Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/"}}}
	response := e.do(t, "PUT", base+"/test", api.SaveAutomationDraftRequest{Definition: spec}, nil)
	if response.Code != http.StatusPaymentRequired {
		t.Fatalf("free plan=%d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "PUT", base+"/test", map[string]any{"definition": spec, "unknown": true}, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown=%d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "DELETE", base+"/test", nil, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing revision=%d %s", response.Code, response.Body.String())
	}
}

func TestAutomationWriteScope(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "read-authoring")
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "read-only", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	spec := api.WorkflowSpec{Name: "draft", Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/"}}}
	response := e.do(t, "GET", "/v1/apps/"+app.Slug+"/automations", nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("read scope=%d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "PUT", "/v1/apps/"+app.Slug+"/automations/draft", api.SaveAutomationDraftRequest{Definition: spec}, nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("read key wrote draft=%d %s", response.Code, response.Body.String())
	}
}
