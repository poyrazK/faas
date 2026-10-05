// adr: 593 — customer admission, durable progress and secret-free projections.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type resizeAPIProvider struct{ capabilityOnlyProvider }

func (resizeAPIProvider) Capabilities() managedpostgres.Capabilities {
	c := capabilityOnlyProvider{}.Capabilities()
	c.ClassResize = true
	return c
}
func (resizeAPIProvider) Provision(_ context.Context, r managedpostgres.ProvisionRequest) (managedpostgres.ObservedDatabase, error) {
	return managedpostgres.ObservedDatabase{ProviderResourceID: "PRIVATE_PROJECT", DataResourceID: "PRIVATE_DATA", Spec: r.Spec, Status: managedpostgres.ProviderStatusReady}, nil
}
func (resizeAPIProvider) Update(_ context.Context, r managedpostgres.UpdateRequest) (managedpostgres.ObservedDatabase, error) {
	return managedpostgres.ObservedDatabase{ProviderResourceID: r.ResourceID, DataResourceID: r.DataResourceID, Spec: r.Spec, Status: managedpostgres.ProviderStatusReady}, nil
}
func resizeAPIService(t *testing.T, account string) (*managedpostgres.Service, managedpostgres.Database, *bool) {
	t.Helper()
	enabled := true
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "us-east-1", MaxDatabasesPerAccount: 3, Defaults: map[string]string{"us-east-1": "PRIVATE_BACKEND"}, Backends: []managedpostgres.BackendConfig{{ID: "PRIVATE_BACKEND", Driver: "fixture", Region: "us-east-1", Namespace: "private"}}}, func(string) string { return "PRIVATE_SECRET" }, map[string]managedpostgres.Factory{"fixture": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
		return resizeAPIProvider{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := managedpostgres.NewService(registry, managedpostgres.NewMemoryStore(), managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return enabled }})
	if err != nil {
		t.Fatal(err)
	}
	d, err := svc.Create(t.Context(), managedpostgres.CreateRequest{AccountID: account, Name: "orders", Spec: managedpostgres.Spec{Region: "us-east-1", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	return svc, d, &enabled
}
func TestManagedPostgresResizeRoutesAndReplay(t *testing.T) {
	e := setup(t, api.PlanPro)
	service, d, enabled := resizeAPIService(t, e.acct.ID)
	e.s.managedPostgres = service
	capabilities := e.do(t, http.MethodGet, "/v1/postgres/capabilities", nil, nil)
	var support api.ManagedPostgresCapabilities
	if err := json.Unmarshal(capabilities.Body.Bytes(), &support); err != nil || !support.ClassResize {
		t.Fatal("resize support not discoverable", err)
	}
	id := uuid.NewString()
	path := "/v1/postgres/databases/" + d.ID + "/resize"
	payload := map[string]any{"request_id": id, "service_class": "burstable"}
	if rec := e.do(t, http.MethodPost, path, payload, map[string]string{"Authorization": ""}); rec.Code != http.StatusUnauthorized {
		t.Fatal("unauthenticated resize", rec.Code)
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
	if rec.Code != http.StatusAccepted || !strings.HasSuffix(rec.Header().Get("Location"), "/resizes/"+id) {
		t.Fatal("reservation", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "PRIVATE_") {
		t.Fatal("private evidence leaked")
	}
	if _, err := service.Reconcile(t.Context(), e.acct.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	*enabled = false
	rec = e.do(t, http.MethodPost, path, payload, nil)
	var view api.ManagedPostgresResize
	if err = json.Unmarshal(rec.Body.Bytes(), &view); err != nil || rec.Code != 202 || view.State != "succeeded" || view.Generation != 2 || !view.ConnectionInterruptionExpected {
		t.Fatal("replay cached initial acceptance", rec.Code, rec.Body.String(), err)
	}
	progress := strings.TrimSuffix(path, "/resize") + "/resizes/" + id
	rec = e.do(t, http.MethodGet, progress, nil, headers)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("read progress", rec.Code, rec.Body.String())
	}
	payload["service_class"] = "development"
	if rec = e.do(t, http.MethodPost, path, payload, nil); rec.Code != 409 {
		t.Fatal("UUID changed target", rec.Code, rec.Body.String())
	}
}
func TestManagedPostgresResizeValidationAndPlan(t *testing.T) {
	e := setup(t, api.PlanHobby)
	e.s.managedPostgres, _, _ = resizeAPIService(t, e.acct.ID)
	path := "/v1/postgres/databases/anything/resize"
	for _, tc := range []struct {
		body   map[string]any
		status int
	}{
		{map[string]any{"request_id": "invalid", "service_class": "development"}, 400},
		{map[string]any{"request_id": uuid.NewString(), "service_class": "unknown"}, 400},
		{map[string]any{"request_id": uuid.NewString(), "service_class": "burstable"}, 403},
		{map[string]any{"request_id": uuid.NewString(), "service_class": "development", "provider_id": "private"}, 400},
	} {
		if rec := e.do(t, http.MethodPost, path, tc.body, nil); rec.Code != tc.status {
			t.Fatal("validation", tc.body, rec.Code, rec.Body.String())
		}
	}
}
