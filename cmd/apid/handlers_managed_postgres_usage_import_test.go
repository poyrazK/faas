// adr: 583 — operator evidence imports use strict mutation authentication.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestManagedPostgresUsageImportRoute(t *testing.T) {
	e := setup(t, api.PlanPro)
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "us-east-1", MaxDatabasesPerAccount: 100, Defaults: map[string]string{"us-east-1": "primary"}, Backends: []managedpostgres.BackendConfig{{ID: "primary", Driver: "fixture", Region: "us-east-1", Namespace: "imports"}}, Usage: managedpostgres.UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600, StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 1000, MaxMonthlyComputeUnitSeconds: 1000, MaxMonthlyStorageByteSeconds: 1000, MaxMonthlyEgressBytes: 1000, ComputeUnitHourMillicents: 3600}}, func(string) string { return "" }, map[string]managedpostgres.Factory{"fixture": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
		return diagnosticsNoProvider{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	store := managedpostgres.NewMemoryStore()
	service, err := managedpostgres.NewService(registry, store, managedpostgres.ServiceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	e.s.managedPostgres = service
	account := uuid.NewString()
	databaseID := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Second)
	from := now.Truncate(time.Hour).Add(-2 * time.Hour)
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	input := managedpostgres.Database{ID: databaseID, AccountID: account, Name: "orders", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, State: managedpostgres.StateProvisioning, CreatedAt: from, UpdatedAt: from, Spec: managedpostgres.Spec{Region: "us-east-1", PostgresMajor: 16, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone}}
	if _, _, err := store.Reserve(context.Background(), input, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(context.Background(), account, databaseID, "initial", managedpostgres.StateProvisioning, from, from.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(context.Background(), databaseID, "initial", "private-provider-id", from.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishProvision(context.Background(), databaseID, "initial", from.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	request := api.ManagedPostgresUsageImportRequest{ImportID: uuid.NewString(), DatabaseID: databaseID, EvidenceReference: "retained/export", EvidenceSHA256: strings.Repeat("a", 64), Reason: "Recover missing window", Windows: []api.ManagedPostgresUsageImportWindow{{From: from, To: from.Add(time.Hour), ObservedAt: now, Readings: []api.ManagedPostgresUsageImportReading{{Meter: "compute_unit_seconds", Quantity: 60}}}}}
	path := "/v1/admin/managed-postgres/accounting/" + account + "/usage-imports"
	if rec := e.do(t, http.MethodPost, path+"/preview", request, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("customer preview=%d", rec.Code)
	}
	e.s.WithAdminAllowlist(e.acct.Email)
	rec := e.do(t, http.MethodPost, path+"/preview", request, nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	var preview api.ManagedPostgresUsageImportResult
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Applied || preview.ImportedCostMillicents != 60 || strings.Contains(rec.Body.String(), "private-provider") {
		t.Fatalf("unsafe preview: %s", rec.Body)
	}
	if rec := e.do(t, http.MethodPost, path, request, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("bearer apply: %d %s", rec.Code, rec.Body)
	}
	request.ExpectedRevision = preview.Revision
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		origin string
		key    string
		want   int
	}{
		{"missing key", "", "", http.StatusBadRequest},
		{"cross origin", "https://attacker.example", "import-test", http.StatusForbidden},
		{"applied", "", "import-test", http.StatusOK},
		{"replayed", "", "import-test-retry", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
			e.addAdminSession(t, r)
			r.Header.Set("Content-Type", "application/json")
			if tc.key != "" {
				r.Header.Set("Idempotency-Key", tc.key)
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
				r.Header.Set("Sec-Fetch-Site", "cross-site")
			}
			rec := httptest.NewRecorder()
			e.h.ServeHTTP(rec, r)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body)
			}
		})
	}
	for _, bad := range []string{`{"unexpected":true}`, string(data) + ` {}`, strings.Repeat(" ", 1<<20) + string(data)} {
		r := httptest.NewRequest(http.MethodPost, path+"/preview", strings.NewReader(bad))
		r.Header.Set("Authorization", "Bearer "+e.key)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid body: %d %s", rec.Code, rec.Body)
		}
	}
}
