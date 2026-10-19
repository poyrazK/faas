// adr: 623 — customer admission, durable progress and secret-free projections.
// adr: 1027 — storage limits grow through the same resize, within the plan.
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/managedpg"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type resizeAPIProvider struct{ capabilityOnlyProvider }

func (resizeAPIProvider) Capabilities() managedpostgres.Capabilities {
	c := capabilityOnlyProvider{}.Capabilities()
	c.ClassResize = true
	c.ScaleToZeroUpdate = true
	c.StorageResize, c.MaxStorageBytes = true, 100<<30
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
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "us-east-1", MaxDatabasesPerAccount: 3, Defaults: map[string]string{"us-east-1": "private-backend"}, Backends: []managedpostgres.BackendConfig{{ID: "private-backend", Driver: "fixture", Region: "us-east-1", Namespace: "private"}}}, func(string) string { return "PRIVATE_SECRET" }, map[string]managedpostgres.Factory{"fixture": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
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
	e := apidserver.Setup(t, api.PlanPro)
	service, d, enabled := resizeAPIService(t, (*e.Acct()).ID)
	(*(*e.S()).ManagedPostgres()) = service
	capabilities := e.Do(t, http.MethodGet, "/v1/postgres/capabilities", nil, nil)
	var support api.ManagedPostgresCapabilities
	if err := json.Unmarshal(capabilities.Body.Bytes(), &support); err != nil || !support.ClassResize {
		t.Fatal("resize support not discoverable", err)
	}
	id := uuid.NewString()
	path := "/v1/postgres/databases/" + d.ID + "/resize"
	payload := map[string]any{"request_id": id, "service_class": "burstable"}
	if rec := e.Do(t, http.MethodPost, path, payload, map[string]string{"Authorization": ""}); rec.Code != http.StatusUnauthorized {
		t.Fatal("unauthenticated resize", rec.Code)
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*e.Store()).CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "read only", []string{"postgres:read"}); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	if rec := e.Do(t, http.MethodPost, path, payload, headers); rec.Code != http.StatusForbidden {
		t.Fatal("read scope mutated", rec.Code)
	}
	rec := e.Do(t, http.MethodPost, path, payload, nil)
	if rec.Code != http.StatusAccepted || !strings.HasSuffix(rec.Header().Get("Location"), "/resizes/"+id) {
		t.Fatal("reservation", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "PRIVATE_") || strings.Contains(rec.Body.String(), "private-backend") {
		t.Fatal("private evidence leaked")
	}
	if _, err := service.Reconcile(t.Context(), (*e.Acct()).ID, d.ID); err != nil {
		t.Fatal(err)
	}
	*enabled = false
	rec = e.Do(t, http.MethodPost, path, payload, nil)
	var view api.ManagedPostgresResize
	if err = json.Unmarshal(rec.Body.Bytes(), &view); err != nil || rec.Code != 202 || view.State != "succeeded" || view.Generation != 2 || !view.ConnectionInterruptionExpected {
		t.Fatal("replay cached initial acceptance", rec.Code, rec.Body.String(), err)
	}
	progress := strings.TrimSuffix(path, "/resize") + "/resizes/" + id
	rec = e.Do(t, http.MethodGet, progress, nil, headers)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("read progress", rec.Code, rec.Body.String())
	}
	payload["service_class"] = "development"
	if rec = e.Do(t, http.MethodPost, path, payload, nil); rec.Code != 409 {
		t.Fatal("UUID changed target", rec.Code, rec.Body.String())
	}
}
func TestManagedPostgresResizeValidationAndPlan(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	(*(*e.S()).ManagedPostgres()), _, _ = resizeAPIService(t, (*e.Acct()).ID)
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
		if rec := e.Do(t, http.MethodPost, path, tc.body, nil); rec.Code != tc.status {
			t.Fatal("validation", tc.body, rec.Code, rec.Body.String())
		}
	}
}
// Storage growth is a preview: outside its accounts the field is unknown
// and nothing mentions storage.
func TestManagedPostgresStorageResizeHiddenOutsideThePreview(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	service, d, _ := resizeAPIService(t, (*e.Acct()).ID)
	(*(*e.S()).ManagedPostgres()) = service
	if rec := e.Do(t, http.MethodGet, "/v1/postgres/capabilities", nil, nil); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "storage_resize") {
		t.Fatal("capabilities mention storage growth", rec.Code, rec.Body.String())
	}
	path := "/v1/postgres/databases/" + d.ID + "/resize"
	unknown := e.Do(t, http.MethodPost, path, map[string]any{"request_id": uuid.NewString(), "service_class": "burstable", "no_such_field": 1}, nil)
	for _, body := range []map[string]any{
		{"request_id": uuid.NewString(), "storage_limit_bytes": 2 << 30},
		{"request_id": uuid.NewString(), "service_class": "burstable", "storage_limit_bytes": 0},
	} {
		if rec := e.Do(t, http.MethodPost, path, body, nil); rec.Code != unknown.Code || rec.Body.String() != unknown.Body.String() {
			t.Fatal("storage outside the preview", body, rec.Code, rec.Body.String(), "want", unknown.Code, unknown.Body.String())
		}
	}
	rec := e.Do(t, http.MethodPost, path, map[string]any{"request_id": uuid.NewString(), "service_class": "burstable"}, nil)
	if rec.Code != http.StatusAccepted || strings.Contains(rec.Body.String(), "storage") {
		t.Fatal("a class resize view mentions storage", rec.Code, rec.Body.String())
	}
}

func TestManagedPostgresStorageResize(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	service, d, _ := resizeAPIService(t, (*e.Acct()).ID)
	(*(*e.S()).ManagedPostgres()) = service
	var err error
	if *(*e.S()).PostgresStorage(), err = managedpg.ParseRollout("TEST", (*e.Acct()).ID); err != nil {
		t.Fatal(err)
	}
	var support api.ManagedPostgresCapabilities
	if err := json.Unmarshal(e.Do(t, http.MethodGet, "/v1/postgres/capabilities", nil, nil).Body.Bytes(), &support); err != nil || !support.StorageResize {
		t.Fatal("storage resize not discoverable", err)
	}
	path := "/v1/postgres/databases/" + d.ID + "/resize"
	for _, tc := range []struct {
		name   string
		body   map[string]any
		status int
	}{
		{"nothing to change", map[string]any{"request_id": uuid.NewString()}, 400},
		{"negative", map[string]any{"request_id": uuid.NewString(), "storage_limit_bytes": -1}, 400},
		{"shrink", map[string]any{"request_id": uuid.NewString(), "storage_limit_bytes": 1 << 29}, 400},
	} {
		if rec := e.Do(t, http.MethodPost, path, tc.body, nil); rec.Code != tc.status {
			t.Fatal(tc.name, rec.Code, rec.Body.String())
		}
	}
	rec := e.Do(t, http.MethodPost, path, map[string]any{"request_id": uuid.NewString(), "storage_limit_bytes": int64(51) << 30}, nil)
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != http.StatusForbidden || problem.Limit == nil || *problem.Limit != 50<<30 ||
		problem.Observed == nil || *problem.Observed != 51<<30 || problem.DocsURL == "" {
		t.Fatal("plan storage limit", rec.Code, rec.Body.String())
	}
	id := uuid.NewString()
	rec = e.Do(t, http.MethodPost, path, map[string]any{"request_id": id, "storage_limit_bytes": 2 << 30}, nil)
	var view api.ManagedPostgresResize
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil || rec.Code != http.StatusAccepted || view.FromClass != "development" || view.TargetClass != "development" ||
		view.FromStorageLimitBytes != 1<<30 || view.TargetStorageLimitBytes != 2<<30 {
		t.Fatal("storage resize", rec.Code, rec.Body.String())
	}
	result, err := service.Reconcile(t.Context(), (*e.Acct()).ID, d.ID)
	if err != nil || result.Spec.StorageLimitBytes != 2<<30 {
		t.Fatal("reconcile", result, err)
	}
	rec = e.Do(t, http.MethodGet, "/v1/postgres/databases/"+d.ID+"/resizes/"+id, nil, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil || rec.Code != http.StatusOK || view.State != "succeeded" || view.TargetStorageLimitBytes != 2<<30 {
		t.Fatal("progress", rec.Code, rec.Body.String())
	}
}
