package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type debugSandboxScenarioStore struct {
	*state.MemStore
	appID string
	row   sqlc.GetRequestTelemetryByAppAndIdentifierRow
}

func (s *debugSandboxScenarioStore) GetRequestTelemetryByAppAndIdentifier(_ context.Context, q sqlc.GetRequestTelemetryByAppAndIdentifierParams) (sqlc.GetRequestTelemetryByAppAndIdentifierRow, error) {
	if q.AppID == stringToPgUUID(s.appID) && q.Identifier == uuidFromPg(s.row.ID) {
		return s.row, nil
	}
	return sqlc.GetRequestTelemetryByAppAndIdentifierRow{}, pgx.ErrNoRows
}

func TestDebugSandboxScenarioDraftAndPrivateSave(t *testing.T) {
	e, app, release := sandboxReleaseFixture(t)
	routes := []state.DeclaredRoute{{Path: "/exports/{id}", Methods: []string{"GET"}}}
	if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{DeclaredRoutes: &routes, SetDeclaredRoutes: true}); err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	store := &debugSandboxScenarioStore{
		MemStore: e.store, appID: app.ID,
		row: sqlc.GetRequestTelemetryByAppAndIdentifierRow{
			ID: stringToPgUUID(requestID), DeploymentID: stringToPgUUID(release.ID),
			Method: "GET", Route: "GET /exports/{id}", Count: 1, Status: 200,
			SpansSummary: []byte(`[{"private":"production-fixture"}]`),
		},
	}
	e.s.store = store
	base := "/v1/apps/" + app.Slug
	draftURL := base + "/debug/requests/" + requestID + "/sandbox-scenario-draft"
	releaseURL := base + "/deployments/" + release.ID
	scenarioURL := releaseURL + "/api-sandbox-scenario"
	var draft api.APISandboxScenarioDraft
	response := e.do(t, http.MethodGet, draftURL, nil, nil)
	readAPISandboxTestResponse(t, response, http.StatusOK, &draft)
	if draft.DeploymentID != release.ID || draft.SourceRequestID != requestID || draft.ExpectedRevision != "" ||
		draft.Method != "GET" || draft.RouteTemplate != "/exports/{id}" ||
		response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "production-fixture") {
		t.Fatalf("unexpected draft: %s", response.Body)
	}
	store.row.Route = "GET /exports/customer@example.com?token=private"
	response = e.do(t, http.MethodGet, draftURL, nil, nil)
	var undeclared api.APISandboxScenarioDraft
	readAPISandboxTestResponse(t, response, http.StatusOK, &undeclared)
	if undeclared.RouteTemplate != "" || strings.Contains(response.Body.String(), "customer@example.com") || strings.Contains(response.Body.String(), "token") {
		t.Fatalf("the draft copied an undeclared path: %s", response.Body)
	}
	doc := []byte(`{"openapi":"3.1.0","info":{"title":"Downloads","version":"1"},"paths":{"/downloads/{id}":{"get":{"responses":{"200":{"description":"OK"}}}}}}`)
	if err := e.store.UpsertAppOpenAPIDoc(t.Context(), app.ID, app.AccountID, doc, 1, "3.1.0"); err != nil {
		t.Fatal(err)
	}
	store.row.Route = "GET /downloads/{id}"
	var fromContract api.APISandboxScenarioDraft
	readAPISandboxTestResponse(t, e.do(t, http.MethodGet, draftURL, nil, nil), http.StatusOK, &fromContract)
	if fromContract.RouteTemplate != "/downloads/{id}" {
		t.Fatalf("stored OpenAPI path missing from draft: %+v", fromContract)
	}
	store.row.Count = 2
	if res := e.do(t, http.MethodGet, draftURL, nil, nil); res.Code != http.StatusConflict {
		t.Fatalf("aggregate request returned %d: %s", res.Code, res.Body)
	}
	store.row.Count = 1
	var before api.APISandboxScenarioResponse
	readAPISandboxTestResponse(t, e.do(t, http.MethodGet, scenarioURL, nil, nil), http.StatusOK, &before)
	if before.Scenario != nil {
		t.Fatal("reading a draft saved a scenario")
	}

	input := api.APISandboxScenarioSaveRequest{
		APISandboxScenarioInput: api.APISandboxScenarioInput{
			Seed: json.RawMessage(`{"exports":[{"id":"export_demo"}]}`),
			APISandboxScenarioGuide: api.APISandboxScenarioGuide{
				Name:     "Export download URL",
				Requests: []api.APISandboxExampleRequest{{Title: "Find export", Method: "GET", Path: "/exports/{id}", ExpectedStatus: 200}},
			},
		},
		SourceRequestID: requestID, ExpectedRevision: &draft.ExpectedRevision,
	}
	if res := e.do(t, http.MethodPut, scenarioURL, input, nil); res.Code != http.StatusBadRequest {
		t.Fatalf("unfilled template returned %d: %s", res.Code, res.Body)
	}
	input.Requests[0].Path = "/exports/export_demo"
	var saved api.APISandboxScenarioResponse
	readAPISandboxTestResponse(t, e.do(t, http.MethodPut, scenarioURL, input, nil), http.StatusOK, &saved)
	if saved.SourceRequestID != requestID || saved.Scenario == nil {
		t.Fatalf("provenance missing from owner save: %+v", saved)
	}
	input.Name = "Stale edit"
	if res := e.do(t, http.MethodPut, scenarioURL, input, nil); res.Code != http.StatusConflict {
		t.Fatalf("stale draft returned %d: %s", res.Code, res.Body)
	}
	var loaded api.APISandboxScenarioResponse
	readAPISandboxTestResponse(t, e.do(t, http.MethodGet, scenarioURL, nil, nil), http.StatusOK, &loaded)
	if loaded.SourceRequestID != requestID || loaded.Scenario == nil || loaded.Scenario.Name != saved.Scenario.Name {
		t.Fatalf("owner scenario changed or lost provenance: %+v", loaded)
	}
	copyURL := releaseURL + "/api-sandboxes/" + uuid.NewString()
	res := e.do(t, http.MethodPut, copyURL, api.APISandboxCreateRequest{ScenarioRevision: saved.Scenario.Revision}, nil)
	if res.Code != http.StatusAccepted || strings.Contains(res.Body.String(), requestID) {
		t.Fatalf("copy returned %d or exposed provenance: %s", res.Code, res.Body)
	}
	if res := e.do(t, http.MethodGet, strings.Replace(draftURL, requestID, uuid.NewString(), 1), nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("absent request returned %d: %s", res.Code, res.Body)
	}
}
