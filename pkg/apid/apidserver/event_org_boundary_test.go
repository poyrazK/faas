package apidserver_test

// adr: 20261025211616

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A creator removed from the company loses its apps' event controls that
// live outside /v1/apps/{slug}: recovery jobs (including cancelling the
// company's workflow runs), schemas on the app's own source, and the event
// preview's view of the app's subscriptions.
func TestRemovedCreatorLosesCompanyEventControls(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	ctx := t.Context()
	creator := (*e.Acct()).ID
	org := apidserver.SeedSharedOrgWithOwner(t, e, "event-boundary", "Event Boundary", api.PlanPro)
	app, err := store.CreateApp(ctx, state.App{AccountID: creator, OrgID: org.ID, Slug: "company-events"})
	if err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "recover",
		DefinitionSnapshot: json.RawMessage(`{"name":"recover","steps":[{"name":"send","run":"send"}]}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusDead, nil, nil); err != nil {
		t.Fatal(err)
	}
	job, err := store.CreateEventRecovery(ctx, creator, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(),
		WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: "recover", Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertEventSubscription(ctx, creator, app.ID, "orders", "order.created", nil); err != nil {
		t.Fatal(err)
	}
	schema := func(version string) api.RegisterEventSchemaRequest {
		return api.RegisterEventSchemaRequest{Source: "app." + app.ID, Type: "order.created", Version: version, Schema: json.RawMessage(`{"type":"object"}`)}
	}
	preview := api.PreviewEventRequest{Source: "orders", Type: "order.created", Data: json.RawMessage(`{}`)}
	candidates := func(t *testing.T) int {
		t.Helper()
		rec := e.DoAdmin(t, http.MethodPost, "/v1/events:preview", preview, nil)
		var out api.PreviewEventResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("POST /v1/events:preview = %d: %s", rec.Code, rec.Body)
		}
		return out.CandidateCount
	}

	// While the creator owns the company, every control works.
	if rec := e.DoAdmin(t, http.MethodGet, "/v1/event-recoveries/"+job.ID, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("owner GET recovery = %d: %s", rec.Code, rec.Body)
	}
	if rec := e.DoAdmin(t, http.MethodPost, "/v1/event-schemas", schema("1"), nil); rec.Code != http.StatusCreated {
		t.Fatalf("owner registers an app.<id> schema = %d: %s", rec.Code, rec.Body)
	}
	if got := candidates(t); got != 1 {
		t.Fatalf("owner preview lists %d subscriptions, want 1", got)
	}

	newOwner, _ := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	if err := store.TransferOrgOwnership(ctx, org.ID, creator, newOwner.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(ctx, org.ID, creator); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/v1/event-recoveries/" + job.ID},
		{http.MethodGet, "/v1/event-recoveries/" + job.ID + "/items"},
		{http.MethodPost, "/v1/event-recoveries/" + job.ID + "/pause"},
		{http.MethodPost, "/v1/event-recoveries/" + job.ID + "/cancel"},
		{http.MethodPost, "/v1/event-recoveries/" + job.ID + "/workflow-cancel"},
	} {
		if rec := e.DoAdmin(t, c.method, c.path, nil, nil); rec.Code != http.StatusNotFound {
			t.Errorf("removed creator %s %s = %d, want 404: %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
	if rec := e.DoAdmin(t, http.MethodPost, "/v1/event-schemas", schema("2"), nil); rec.Code != http.StatusForbidden {
		t.Errorf("removed creator registers an app.<id> schema = %d, want 403: %s", rec.Code, rec.Body)
	}
	if got := candidates(t); got != 0 {
		t.Errorf("removed creator's preview lists %d company subscriptions, want 0", got)
	}
}
