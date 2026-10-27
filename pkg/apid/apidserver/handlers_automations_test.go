package apidserver_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAutomationOutboundValidationAndPublication(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "outbound-authoring")
	ctx := context.Background()
	if _, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:x", Workflows: json.RawMessage(`[]`)}); err != nil {
		t.Fatal(err)
	}
	offer := state.OutboundIntegrationOffer{ID: uuid.NewString(), AccountID: app.AccountID, Name: "crm", Origin: "https://api.example.com", AllowedMethods: []string{"POST"}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true, OwnerKind: "customer", CredentialSource: "customer_sealed", RequestPolicy: api.DefaultOutboundRequestPolicy()}
	if _, err := (*e.Store()).CreateOutboundIntegration(ctx, offer); err != nil {
		t.Fatal(err)
	}
	spec := api.WorkflowSpec{Name: "crm", Steps: []api.WorkflowStepSpec{{Name: "send", Outbound: &api.WorkflowOutboundSpec{IntegrationID: offer.ID, Method: "POST", Path: "/v1/contacts"}, Input: json.RawMessage(`{"email":"{{input.email}}"}`)}}}
	base := "/v1/apps/" + app.Slug + "/automations"
	response := e.Do(t, "POST", base+":validate", api.ValidateAutomationRequest{Definition: spec}, nil)
	var validation api.ValidateAutomationResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &validation) != nil || validation.Valid || len(validation.Issues) == 0 {
		t.Fatalf("accepted an unbound credential: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, "PUT", base+"/crm", api.SaveAutomationDraftRequest{Definition: spec}, nil)
	var draft api.AutomationResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &draft) != nil || draft.Draft.Steps[0].Outbound == nil {
		t.Fatalf("outbound draft lost: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, "POST", base+"/crm/publish", api.PublishAutomationRequest{ExpectedVersion: draft.Version}, nil)
	if response.Code != 422 {
		t.Fatalf("published without binding: %d %s", response.Code, response.Body.String())
	}
	if err := (*e.Store()).SetOutboundCredential(ctx, app.AccountID, offer.ID, []byte("sealed-placeholder")); err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).BindOutboundIntegration(ctx, app.AccountID, app.ID, offer.ID); err != nil {
		t.Fatal(err)
	}
	response = e.Do(t, "POST", base+":validate", api.ValidateAutomationRequest{Definition: spec}, nil)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &validation) != nil || !validation.Valid {
		t.Fatalf("bound validation: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, "POST", base+"/crm/publish", api.PublishAutomationRequest{ExpectedVersion: draft.Version}, nil)
	if response.Code != 200 {
		t.Fatalf("bound publication: %d %s", response.Code, response.Body.String())
	}
}

func TestAutomationAPI(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	(*e.S()).WithWorkflowRuntimeEnabled(true)
	app := seedWorkflowApp(t, e, "authoring-api")
	_, err := (*e.Store()).CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:x", Workflows: json.RawMessage(`[]`)})
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/" + app.Slug + "/automations"
	spec := api.WorkflowSpec{Name: "invoice", Trigger: &api.WorkflowTriggerSpec{Type: "event", Source: "billing", EventType: "invoice.paid"}, Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/invoice", When: &api.WorkflowGuardSpec{Ref: "input.invoice_id", Op: "exists", Value: json.RawMessage("true")}}}}
	response := e.Do(t, "POST", base+":validate", api.ValidateAutomationRequest{Definition: spec}, nil)
	var valid api.ValidateAutomationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &valid) != nil || !valid.Valid {
		t.Fatalf("validate=%d %s", response.Code, response.Body.String())
	}
	rows, _ := (*e.Store()).ListAutomations(context.Background(), app.ID)
	if len(rows) != 0 {
		t.Fatal("validation persisted a draft")
	}
	response = e.Do(t, "PUT", base+"/invoice", api.SaveAutomationDraftRequest{Definition: spec}, nil)
	var draft api.AutomationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &draft) != nil || draft.Version == 0 || draft.Published != nil {
		t.Fatalf("save=%d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, "PUT", base+"/invoice", api.SaveAutomationDraftRequest{Definition: spec}, nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("CAS=%d %s", response.Code, response.Body.String())
	}
	checkedSnapshot, _ := json.Marshal(draft.Draft)
	evidence := &api.AutomationCheckEvidence{DefinitionHash: fmt.Sprintf("%x", sha256.Sum256(checkedSnapshot)), CheckedVersion: draft.Version, CheckedAt: time.Now().UTC(), CoveragePassed: true, Scenarios: []api.AutomationCheckScenario{{Name: "success", Passed: true, DefinitionValid: true, Complete: true}}, Exclusions: []api.AutomationCheckExclusion{}}
	bad := *evidence
	bad.DefinitionHash = "mismatch"
	response = e.Do(t, "POST", base+"/invoice/publish", api.PublishAutomationRequest{ExpectedVersion: draft.Version, CheckEvidence: &bad}, nil)
	if response.Code != http.StatusBadRequest && response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mismatched checks accepted: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, "POST", base+"/invoice/publish", map[string]any{"expected_version": draft.Version, "check_evidence": map[string]any{"sample_payload": "private"}}, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown evidence payload accepted: %d", response.Code)
	}
	publishRequest := api.PublishAutomationRequest{ExpectedVersion: draft.Version, CheckEvidence: evidence}
	response = e.Do(t, "POST", base+"/invoice/publish", publishRequest, map[string]string{"Idempotency-Key": "publish-invoice"})
	var published api.AutomationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &published) != nil || published.PublishedVersion == 0 || published.Source != "dashboard" {
		t.Fatalf("publish=%d %s", response.Code, response.Body.String())
	}
	if published.Published == nil || published.Published.Steps[0].When == nil || published.Published.Steps[0].When.Ref != "input.invoice_id" {
		t.Fatal("publication lost guard")
	}
	replay := e.Do(t, "POST", base+"/invoice/publish", publishRequest, map[string]string{"Idempotency-Key": "publish-invoice"})
	if replay.Code != http.StatusOK || replay.Body.String() != response.Body.String() {
		t.Fatalf("publish replay=%d %s", replay.Code, replay.Body.String())
	}
	response = e.Do(t, "GET", base+"/invoice/revisions?limit=1&offset=0", nil, nil)
	var revisionPage api.ListAutomationRevisionsResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &revisionPage) != nil || revisionPage.Total != 1 || len(revisionPage.Revisions) != 1 {
		t.Fatalf("revision list=%d %s", response.Code, response.Body.String())
	}
	firstRevision := revisionPage.Revisions[0]
	if firstRevision.CheckEvidence == nil || firstRevision.CheckEvidence.DefinitionHash != firstRevision.DefinitionHash || firstRevision.CheckEvidence.Scenarios[0].Name != "success" {
		t.Fatalf("evidence not retained: %+v", firstRevision)
	}
	if firstRevision.Version != published.PublishedVersion || firstRevision.LegacySnapshot || len(firstRevision.DefinitionHash) != 64 || firstRevision.PublishedByAccountID != app.AccountID {
		t.Fatalf("revision metadata=%+v", firstRevision)
	}
	response = e.Do(t, "GET", base+"/invoice/revisions/"+strconv.FormatInt(firstRevision.Version, 10), nil, nil)
	var fetchedRevision api.AutomationRevisionResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &fetchedRevision) != nil || fetchedRevision.Definition.Steps[0].Path != "/invoice" {
		t.Fatalf("revision get=%d %s", response.Code, response.Body.String())
	}

	newDefinition := api.WorkflowSpec{Name: "invoice", Trigger: spec.Trigger, Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/invoice-v2"}}}
	response = e.Do(t, "PUT", base+"/invoice", api.SaveAutomationDraftRequest{ExpectedVersion: published.Version, Definition: newDefinition}, nil)
	var newerDraft api.AutomationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &newerDraft) != nil {
		t.Fatalf("save v2=%d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, "POST", base+"/invoice/publish", api.PublishAutomationRequest{ExpectedVersion: newerDraft.Version}, map[string]string{"Idempotency-Key": "publish-invoice-v2"})
	var newerPublished api.AutomationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &newerPublished) != nil || newerPublished.PublishedVersion == published.PublishedVersion {
		t.Fatalf("publish v2=%d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, "POST", base+"/invoice/revisions/"+strconv.FormatInt(firstRevision.Version, 10)+"/restore", api.RestoreAutomationRevisionRequest{ExpectedVersion: newerPublished.Version}, map[string]string{"Idempotency-Key": "restore-invoice-v1"})
	var restored api.AutomationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &restored) != nil || restored.Draft.Steps[0].Path != "/invoice" || restored.Published == nil || restored.Published.Steps[0].Path != "/invoice-v2" || restored.PublishedVersion != newerPublished.PublishedVersion {
		t.Fatalf("restore=%d %s", response.Code, response.Body.String())
	}
	staleRestore := e.Do(t, "POST", base+"/invoice/revisions/"+strconv.FormatInt(firstRevision.Version, 10)+"/restore", api.RestoreAutomationRevisionRequest{ExpectedVersion: newerPublished.Version}, map[string]string{"Idempotency-Key": "restore-invoice-v1-stale"})
	if staleRestore.Code != http.StatusConflict {
		t.Fatalf("stale restore=%d %s", staleRestore.Code, staleRestore.Body.String())
	}
	response = e.Do(t, "GET", base+"/invoice/revisions?limit=10", nil, nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &revisionPage) != nil || revisionPage.Total != 2 || len(revisionPage.Revisions) != 2 || revisionPage.Revisions[0].Version != newerPublished.PublishedVersion {
		t.Fatalf("revision history=%d %s", response.Code, response.Body.String())
	}
	paused := false
	response = e.Do(t, "PUT", base+"/invoice/enabled", api.SetAutomationEnabledRequest{ExpectedVersion: restored.Version, Enabled: &paused}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("pause=%d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, "PUT", base+"/invoice/enabled", map[string]any{"expected_version": restored.Version}, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled=%d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, "POST", "/v1/apps/"+app.Slug+"/workflows/invoice/runs", map[string]any{"invoice_id": "123"}, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("run=%d %s", response.Code, response.Body.String())
	}
	runs, _, _ := (*e.Store()).ListWorkflowRuns(context.Background(), app.ID, state.ListWorkflowRunsOpts{Limit: 10})
	if len(runs) != 1 {
		t.Fatal("missing published run")
	}
	var snapshot api.WorkflowSpec
	_ = json.Unmarshal(runs[0].DefinitionSnapshot, &snapshot)
	if snapshot.Steps[0].Path != "/invoice-v2" {
		t.Fatalf("snapshot=%s", runs[0].DefinitionSnapshot)
	}
	// Authenticated customers cannot inspect or mutate a different account's app.
	other, _ := (*e.Store()).CreateAccount(context.Background(), "other-author@example.com", api.PlanHobby)
	otherApp, _ := (*e.Store()).CreateApp(context.Background(), state.App{AccountID: other.ID, Slug: "other-author", Type: state.AppTypeApp})
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		path := "/v1/apps/" + otherApp.Slug + "/automations/invoice"
		if method == "DELETE" {
			path += "?expected_version=1"
		}
		response = e.Do(t, method, path, api.SaveAutomationDraftRequest{Definition: spec}, nil)
		if response.Code != http.StatusNotFound {
			t.Fatalf("isolation %s=%d %s", method, response.Code, response.Body.String())
		}
	}
}

func TestAutomationHealthSummaryIsBoundedAndOmitsRunData(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	(*e.S()).WithWorkflowRuntimeEnabled(true)
	app := seedWorkflowApp(t, e, "automation-health")
	if _, err := (*e.Store()).CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:x", Workflows: json.RawMessage(`[]`)}); err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/" + app.Slug + "/automations"
	draft := e.Do(t, http.MethodPut, base+"/invoice", api.SaveAutomationDraftRequest{Definition: api.WorkflowSpec{Name: "invoice", Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/invoice"}}}}, nil)
	if draft.Code != http.StatusOK {
		t.Fatalf("save automation: %d %s", draft.Code, draft.Body.String())
	}
	ctx := context.Background()
	createRun := func(status string) *state.WorkflowRun {
		t.Helper()
		run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "invoice", Input: json.RawMessage(`{"private_input":"health-secret"}`), DefinitionSnapshot: json.RawMessage(`{"name":"invoice","steps":[]}`)}
		if err := (*e.Store()).CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err := (*e.Store()).MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusRunning, nil, nil); err != nil {
			t.Fatal(err)
		}
		if status == state.WorkflowRunStatusFailed {
			if err := (*e.Store()).CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "main", Status: state.WorkflowStepStatusFailed}}); err != nil {
				t.Fatal(err)
			}
		}
		privateError := "health-private-error"
		if err := (*e.Store()).MarkWorkflowRunStatus(ctx, run.ID, status, json.RawMessage(`{"private_output":"health-output-secret"}`), &privateError); err != nil {
			t.Fatal(err)
		}
		return run
	}
	createRun(state.WorkflowRunStatusSucceeded)
	createRun(state.WorkflowRunStatusFailed)

	response := e.Do(t, http.MethodGet, base+"/invoice/health", nil, nil)
	var health api.AutomationHealthResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &health) != nil {
		t.Fatalf("automation health: %d %s", response.Code, response.Body.String())
	}
	if health.RunCount != 2 || health.CompletedRunCount != 2 || health.SuccessRate != 0.5 || health.StatusCounts[state.WorkflowRunStatusSucceeded] != 1 || health.StatusCounts[state.WorkflowRunStatusFailed] != 1 || len(health.FailedSteps) != 1 || health.FailedSteps[0].StepName != "main" {
		t.Fatalf("automation health = %+v", health)
	}
	if health.WindowEnd.Sub(health.WindowStart) != api.WorkflowAutomationHealthDefaultRange {
		t.Fatalf("default health window = %s", health.WindowEnd.Sub(health.WindowStart))
	}
	if health.Queue == nil || health.Queue.WaitingRunCount != 0 || health.Queue.AppRunningCount != 0 || len(health.Queue.ReasonCounts) != 7 {
		t.Fatalf("empty current queue = %+v", health.Queue)
	}
	initialBody := response.Body.String()
	queued := &state.WorkflowRun{AppID: app.ID, WorkflowName: "invoice", Input: json.RawMessage(`{"private_input":"health-secret"}`), DefinitionSnapshot: json.RawMessage(`{"name":"invoice","steps":[]}`)}
	if err := (*e.Store()).CreateWorkflowRun(ctx, queued); err != nil {
		t.Fatal(err)
	}
	after := url.QueryEscape(time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano))
	before := url.QueryEscape(time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano))
	response = e.Do(t, http.MethodGet, base+"/invoice/health?created_after="+after+"&created_before="+before, nil, nil)
	health = api.AutomationHealthResponse{}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &health) != nil || health.RunCount != 0 || health.Queue == nil || health.Queue.WaitingRunCount != 1 || health.Queue.ReasonCounts[api.AutomationQueueReady] != 1 {
		t.Fatalf("historical window hid current queue: %d %s", response.Code, response.Body.String())
	}
	for _, secret := range []string{"health-secret", "health-output-secret", "health-private-error"} {
		if strings.Contains(initialBody+response.Body.String(), secret) {
			t.Fatalf("health endpoint leaked %q: %s", secret, response.Body.String())
		}
	}

	future := url.QueryEscape(time.Now().UTC().Add(time.Hour).Format(time.RFC3339))
	response = e.Do(t, http.MethodGet, base+"/invoice/health?created_before="+future, nil, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("future health window: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, http.MethodGet, base+"/missing/health", nil, nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown automation health: %d %s", response.Code, response.Body.String())
	}
	other, err := (*e.Store()).CreateAccount(ctx, "queue-owner@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: other.ID, Slug: "foreign-queue", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	response = e.Do(t, http.MethodGet, "/v1/apps/"+foreign.Slug+"/automations/invoice/health", nil, nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("foreign automation health exposed: %d %s", response.Code, response.Body.String())
	}
}

func TestAutomationAPIBoundsAndPlan(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	app := seedWorkflowApp(t, e, "free-authoring")
	base := "/v1/apps/" + app.Slug + "/automations"
	spec := api.WorkflowSpec{Name: "test", Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/"}}}
	response := e.Do(t, "PUT", base+"/test", api.SaveAutomationDraftRequest{Definition: spec}, nil)
	if response.Code != http.StatusPaymentRequired {
		t.Fatalf("free plan=%d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, "PUT", base+"/test", map[string]any{"definition": spec, "unknown": true}, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown=%d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, "DELETE", base+"/test", nil, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing revision=%d %s", response.Code, response.Body.String())
	}
}

func TestAutomationWriteScope(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "read-authoring")
	key, hash, _ := api.GenerateAPIKey()
	if _, err := (*e.Store()).CreateAPIKey(context.Background(), (*e.Acct()).ID, hash, "read-only", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	(*e.Key()) = key
	spec := api.WorkflowSpec{Name: "draft", Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/"}}}
	response := e.Do(t, "GET", "/v1/apps/"+app.Slug+"/automations", nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("read scope=%d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/automations/draft", api.SaveAutomationDraftRequest{Definition: spec}, nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("read key wrote draft=%d %s", response.Code, response.Body.String())
	}
}

// adr: 20261027090010 — an automation may run an agent only while the agents
// capability is released to its account; elsewhere an agent step is refused
// like any invalid step target.
func TestAutomationAgentStepFollowsAgentsCapability(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).WithWorkflowRuntimeEnabled(true)
	installAgentGate(t, *e.S(), "automations")
	app := seedWorkflowApp(t, e, "agent-step-api")
	base := "/v1/apps/" + app.Slug + "/automations"
	spec := api.WorkflowSpec{Name: "triage", Steps: []api.WorkflowStepSpec{{Name: "review", Agent: "pr-triage"}}}
	var valid api.ValidateAutomationResponse
	response := e.Do(t, "POST", base+":validate", api.ValidateAutomationRequest{Definition: spec}, nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &valid) != nil || valid.Valid ||
		strings.Contains(response.Body.String(), "agent") {
		t.Fatalf("validate with agents off: %d %s", response.Code, response.Body)
	}
	if response := e.Do(t, "PUT", base+"/triage", api.SaveAutomationDraftRequest{Definition: spec}, nil); response.Code == http.StatusOK {
		t.Fatalf("save with agents off: %d %s", response.Code, response.Body)
	}
	releaseAgentsTo(t, *e.S(), (*e.Acct()).ID)
	response = e.Do(t, "POST", base+":validate", api.ValidateAutomationRequest{Definition: spec}, nil)
	valid = api.ValidateAutomationResponse{}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &valid) != nil || !valid.Valid {
		t.Fatalf("validate with agents released: %d %s", response.Code, response.Body)
	}
	if response := e.Do(t, "PUT", base+"/triage", api.SaveAutomationDraftRequest{Definition: spec}, nil); response.Code != http.StatusOK {
		t.Fatalf("save with agents released: %d %s", response.Code, response.Body)
	}
}
