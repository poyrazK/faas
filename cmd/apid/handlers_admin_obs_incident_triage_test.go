package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestObsIncidentTriage_PersistsOperatorMetadata(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	req := httptest.NewRequest(http.MethodPut, "/v1/admin/obs/incidents/deployment:d1/triage", bytes.NewBufferString(`{"status":"acknowledged","reason":"triage_started","note":"investigating"}`))
	req.SetPathValue("dedupe_key", "deployment:d1")
	rec := httptest.NewRecorder()
	e.s.putObsIncidentTriage(rec, req, e.acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var response api.ObsIncidentTriageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Triage.Status != apiTriageAcknowledged || response.Triage.Owner != "ops@faas.dev" {
		t.Fatalf("unexpected triage response: %+v", response.Triage)
	}
	rows, err := e.store.ListOperatorIncidentTriage(context.Background(), []string{"deployment:d1"})
	if err != nil {
		t.Fatal(err)
	}
	if rows["deployment:d1"].Note != "investigating" {
		t.Fatalf("stored note = %q", rows["deployment:d1"].Note)
	}
}

func TestObsIncidentTriage_RejectsInvalidReason(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	req := httptest.NewRequest(http.MethodPut, "/v1/admin/obs/incidents/deployment:d1/triage", bytes.NewBufferString(`{"status":"resolved","reason":"bad reason"}`))
	req.SetPathValue("dedupe_key", "deployment:d1")
	rec := httptest.NewRecorder()
	e.s.putObsIncidentTriage(rec, req, e.acct)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

const apiTriageAcknowledged = "acknowledged"
