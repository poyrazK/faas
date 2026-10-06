// adr: 624 — required explicit policy, access scopes and durable replay.
package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestManagedPostgresComputePolicyRoutesAndReplay(t *testing.T) {
	e := setup(t, api.PlanPro)
	service, d, enabled := resizeAPIService(t, e.acct.ID)
	e.s.managedPostgres = service
	capabilities := e.do(t, http.MethodGet, "/v1/postgres/capabilities", nil, nil)
	var support api.ManagedPostgresCapabilities
	if err := json.Unmarshal(capabilities.Body.Bytes(), &support); err != nil || !support.ScaleToZeroUpdate {
		t.Fatal("policy support not discoverable", err)
	}
	id := uuid.NewString()
	path := "/v1/postgres/databases/" + d.ID + "/compute-policy"
	payload := map[string]any{"request_id": id, "scale_to_zero": false}
	if rec := e.do(t, http.MethodPost, path, payload, map[string]string{"Authorization": ""}); rec.Code != http.StatusUnauthorized {
		t.Fatal("unauthenticated policy change", rec.Code)
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "read only", []string{"postgres:read"}); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	if rec := e.do(t, http.MethodPost, path, payload, headers); rec.Code != http.StatusForbidden {
		t.Fatal("read scope mutated", rec.Code)
	}
	rec := e.do(t, http.MethodPost, path, payload, nil)
	if rec.Code != http.StatusAccepted || !strings.HasSuffix(rec.Header().Get("Location"), "/compute-policy-changes/"+id) {
		t.Fatal("reservation", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "PRIVATE_") || strings.Contains(rec.Body.String(), "private-backend") {
		t.Fatal("private evidence leaked")
	}
	if _, err := service.Reconcile(t.Context(), e.acct.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	*enabled = false
	rec = e.do(t, http.MethodPost, path, payload, nil)
	var view api.ManagedPostgresComputePolicyChange
	if err = json.Unmarshal(rec.Body.Bytes(), &view); err != nil || rec.Code != 202 || view.State != "succeeded" || view.Generation != 2 || !view.ConnectionInterruptionExpected {
		t.Fatal("replay cached initial acceptance", rec.Code, rec.Body.String(), err)
	}
	progress := strings.TrimSuffix(path, "/compute-policy") + "/compute-policy-changes/" + id
	rec = e.do(t, http.MethodGet, progress, nil, headers)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("read progress", rec.Code, rec.Body.String())
	}
	payload["scale_to_zero"] = true
	if rec = e.do(t, http.MethodPost, path, payload, nil); rec.Code != 409 {
		t.Fatal("UUID changed target", rec.Code, rec.Body.String())
	}
}
func TestManagedPostgresComputePolicyValidationAndPlan(t *testing.T) {
	e := setup(t, api.PlanHobby)
	e.s.managedPostgres, _, _ = resizeAPIService(t, e.acct.ID)
	path := "/v1/postgres/databases/anything/compute-policy"
	for _, tc := range []struct {
		body   map[string]any
		status int
	}{
		{map[string]any{"request_id": "invalid", "scale_to_zero": true}, 400},
		{map[string]any{"request_id": uuid.NewString()}, 400},
		{map[string]any{"request_id": uuid.NewString(), "scale_to_zero": nil}, 400},
		{map[string]any{"request_id": uuid.NewString(), "scale_to_zero": false}, 403},
		{map[string]any{"request_id": uuid.NewString(), "scale_to_zero": true, "provider_id": "private"}, 400},
	} {
		if rec := e.do(t, http.MethodPost, path, tc.body, nil); rec.Code != tc.status {
			t.Fatal(tc.body, rec.Code, rec.Body.String())
		}
	}
}
