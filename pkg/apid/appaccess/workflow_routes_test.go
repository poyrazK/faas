package appaccess

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func workflowRunFixture(t *testing.T, store *state.MemStore, app state.App) *state.WorkflowRun {
	t.Helper()
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "order", Input: json.RawMessage(`{}`), DefinitionSnapshot: json.RawMessage(`{"name":"order"}`)}
	if err := store.CreateWorkflowRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	return run
}

// Workflow run URLs name no app, so they used to skip the organization check
// and fall back to a creator-account comparison (Hunt 13 H13-2).
func TestWorkflowRoutesFollowOrganizationRoles(t *testing.T) {
	for _, tc := range []struct {
		role          state.OrgRole
		pattern, path string
		want          int
	}{
		{state.OrgRoleViewer, "GET /v1/workflows/runs/{id}", "/v1/workflows/runs/RUN", 204},
		{state.OrgRoleViewer, "GET /v1/workflows/runs/{id}/steps", "/v1/workflows/runs/RUN/steps", 204},
		{state.OrgRoleViewer, "POST /v1/workflows/runs/{id}/events", "/v1/workflows/runs/RUN/events", 403},
		{state.OrgRoleViewer, "POST /v1/apps/{slug}/workflows/{name}/runs", "/v1/apps/team-api/workflows/order/runs", 403},
		{state.OrgRoleViewer, "GET /v1/apps/{slug}/workflows/runs", "/v1/apps/team-api/workflows/runs", 204},
		{state.OrgRoleDeveloper, "POST /v1/workflows/runs/{id}/events", "/v1/workflows/runs/RUN/events", 204},
		{state.OrgRoleDeveloper, "POST /v1/workflows/runs/{id}/cancel", "/v1/workflows/runs/RUN/cancel", 204},
		{state.OrgRoleDeveloper, "POST /v1/apps/{slug}/workflows/{name}/runs", "/v1/apps/team-api/workflows/order/runs", 204},
		{state.OrgRoleDeveloper, "PUT /v1/workflows/runs/{id}/callbacks/{callback_id}/webhook-binding", "/v1/workflows/runs/RUN/callbacks/cb/webhook-binding", 204},
		{state.OrgRoleBilling, "GET /v1/workflows/runs/{id}", "/v1/workflows/runs/RUN", 403},
	} {
		t.Run(string(tc.role)+"/"+tc.pattern, func(t *testing.T) {
			store, owner, actor, org, app := fixture(t, tc.role)
			run := workflowRunFixture(t, store, app)
			r := request(actor, &state.APIKey{OrgID: org.ID}, tc.pattern, strings.ReplaceAll(tc.path, "RUN", run.ID), app.Slug, run.ID)
			w := httptest.NewRecorder()
			Wrap(store, nil, func(w http.ResponseWriter, r *http.Request, subject state.Account) {
				grant, ok := From(r)
				if !ok || grant.App.ID != app.ID || subject.ID != owner.ID {
					t.Fatal("workflow route reached its handler without the run's app grant")
				}
				w.WriteHeader(http.StatusNoContent)
			})(w, r, actor)
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s; want %d", w.Code, w.Body, tc.want)
			}
		})
	}
}

// An unknown run is left to its handler, which answers the same 404.
func TestWorkflowRunRoutesRefuseRemovedCreator(t *testing.T) {
	store, _, actor, org, app := fixture(t, state.OrgRoleDeveloper)
	run := workflowRunFixture(t, store, app)
	creator, err := store.AccountByID(t.Context(), app.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.TransferOrgOwnership(t.Context(), org.ID, creator.ID, actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(t.Context(), org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		actor state.Account
		runID string
	}{
		{"removed creator", creator, run.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := request(tc.actor, nil, "POST /v1/workflows/runs/{id}/events", "/v1/workflows/runs/"+tc.runID+"/events", "", tc.runID)
			w := httptest.NewRecorder()
			Wrap(store, nil, func(http.ResponseWriter, *http.Request, state.Account) { t.Fatal("unauthorized handler ran") })(w, r, tc.actor)
			if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), api.CodeWorkflowRunNotFound) {
				t.Fatalf("status=%d body=%s; want the workflow run 404", w.Code, w.Body)
			}
		})
	}
}
