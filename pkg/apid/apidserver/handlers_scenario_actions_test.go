package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestScenarioActionPolicyRegistrationAcknowledgesCapture(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	const runID = "abababababababababababababababab"
	rec := e.Do(t, "PUT", "/v1/dev/sessions/capture-api", api.UpsertDevSessionRequest{WorkspaceID: runID}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("session: %d %s", rec.Code, rec.Body.String())
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	req := api.RegisterScenarioTestRequest{WebhookMode: "live", Members: []api.ScenarioTestWorkload{{Workload: "primary", AppSlug: session.App.Slug}}}
	rec = e.Do(t, "PUT", "/v1/dev/test-runs/"+runID, req, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid policy accepted: %d %s", rec.Code, rec.Body.String())
	}
	req.WebhookMode = "capture"
	req.ActionEvidenceVersion = 2
	rec = e.Do(t, "PUT", "/v1/dev/test-runs/"+runID, req, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown evidence version accepted: %d %s", rec.Code, rec.Body.String())
	}
	req.ActionEvidenceVersion = api.ScenarioActionSnapshotVersion
	rec = e.Do(t, "PUT", "/v1/dev/test-runs/"+runID, req, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("capture registration: %d %s", rec.Code, rec.Body.String())
	}
	var ack api.RegisterScenarioTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ack); err != nil || ack.WebhookMode != "capture" || ack.PolicyVersion != 1 || ack.ActionEvidenceVersion != api.ScenarioActionSnapshotVersion {
		t.Fatalf("missing durable acknowledgement: %+v %v", ack, err)
	}
	rec = e.Do(t, "PUT", "/v1/dev/test-runs/"+runID, req, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("capture policy could be replaced: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.Do(t, "POST", "/v1/dev/test-runs/"+runID+"/actions/seal", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("seal: %d %s", rec.Code, rec.Body.String())
	}
	var snapshot api.ScenarioActionSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil || snapshot.Status != "complete" || snapshot.SealedAt == "" || snapshot.RunID != runID || snapshot.CapturedCount != 0 || len(snapshot.Workloads) != 1 {
		t.Fatalf("invalid seal: %+v %v", snapshot, err)
	}
	retained := rec.Body.String()
	rec = e.Do(t, "DELETE", "/v1/dev/sessions/capture-api?workspace_id="+runID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("destroy session: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.Do(t, "DELETE", "/v1/dev/test-runs/"+runID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("cleanup: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.Do(t, "POST", "/v1/dev/test-runs/"+runID+"/actions/seal", nil, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != retained {
		t.Fatalf("cleanup removed sealed snapshot: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.Do(t, "POST", "/v1/dev/test-runs/cccccccccccccccccccccccccccccccc/actions/seal", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown run seal: %d %s", rec.Code, rec.Body.String())
	}
}
