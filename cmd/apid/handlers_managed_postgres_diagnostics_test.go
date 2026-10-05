// adr: 582 — diagnostics are local, operator-only, and do not reveal provider IDs.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// Any provider call panics through the nil embedded interface.
type diagnosticsNoProvider struct{ managedpostgres.Provider }

func (diagnosticsNoProvider) Capabilities() managedpostgres.Capabilities {
	return managedpostgres.Capabilities{
		PostgresMajors: []int{16}, ServiceClasses: []managedpostgres.ServiceClass{managedpostgres.ClassDevelopment}, Availability: []managedpostgres.Availability{managedpostgres.AvailabilitySingleZone}, CredentialAccess: []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite}, UsageMeters: []managedpostgres.Meter{managedpostgres.MeterComputeUnitSeconds}}
}

type diagnosticsAccountStore struct {
	state.Store
	target  state.Account
	lookups int
}

func (s *diagnosticsAccountStore) AccountByID(ctx context.Context, id string) (state.Account, error) {
	s.lookups++
	if id == s.target.ID {
		return s.target, nil
	}
	return s.Store.AccountByID(ctx, id)
}

func TestManagedPostgresAccountingDiagnosticsRoute(t *testing.T) {
	e := setup(t, api.PlanPro)
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "us-east-1", MaxDatabasesPerAccount: 100, Defaults: map[string]string{"us-east-1": "primary"}, Backends: []managedpostgres.BackendConfig{{ID: "primary", Driver: "fixture", Region: "us-east-1", Namespace: "diagnostics"}}, Usage: managedpostgres.UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600, StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 1000, MaxMonthlyComputeUnitSeconds: 1000, MaxMonthlyStorageByteSeconds: 1000, MaxMonthlyEgressBytes: 1000}}, func(string) string { return "" }, map[string]managedpostgres.Factory{"fixture": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
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
	target := e.acct
	target.ID = uuid.NewString()
	catalog := &diagnosticsAccountStore{Store: e.store, target: target}
	e.s.store = catalog
	path := "/v1/admin/managed-postgres/accounting/" + target.ID
	if rec := e.do(t, http.MethodGet, path, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("nonoperator: %d %s", rec.Code, rec.Body)
	}
	if catalog.lookups != 0 {
		t.Fatal("denied operator resolved tenant")
	}
	e.s.WithAdminAllowlist(e.acct.Email)
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "usage-only", []string{"usage:read"}); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + plain}); rec.Code != http.StatusForbidden {
		t.Fatalf("missing admin scope: %d %s", rec.Code, rec.Body)
	}
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=x", "?after=bad"} {
		if rec := e.do(t, http.MethodGet, path+query, nil, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid query: %d %s", rec.Code, rec.Body)
		}
	}
	if catalog.lookups != 0 {
		t.Fatal("invalid pagination resolved tenant")
	}
	rec := e.do(t, http.MethodGet, path+"?limit=2", nil, nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("report: %d %s", rec.Code, rec.Body)
	}
	var page api.ManagedPostgresAccountingDiagnosticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || page.AccountID != target.ID || page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("report: %+v %v", page, err)
	}
	if rec := e.do(t, http.MethodGet, "/v1/admin/managed-postgres/accounting/"+uuid.NewString(), nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing account: %d %s", rec.Code, rec.Body)
	}
	if _, err := e.store.SetMFARequired(context.Background(), e.acct.ID, true); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	sid := uuid.NewString()
	if _, err := e.store.CreateSession(req.Context(), sid, e.acct.ID, "192.0.2.10", "test"); err != nil {
		t.Fatal(err)
	}
	token, err := e.s.sessions.IssueWithSessionAndBindingHashAndStepUp(sid, e.acct.ID, "", time.Time{}, true)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeMFARequired) {
		t.Fatalf("MFA missing: %d %s", rec.Code, rec.Body)
	}
}

func TestManagedPostgresAccountingDiagnosticsProjection(t *testing.T) {
	at := time.Date(2026, 10, 5, 12, 0, 0, 0, time.FixedZone("local", 3*3600))
	view := managedPostgresAccountingDiagnosticsView(managedpostgres.AccountingDiagnostics{AccountID: "account", EvaluatedAt: at, PolicyEnabled: true, Window: time.Hour, Items: []managedpostgres.AccountingDiagnostic{{AccountingCoverage: managedpostgres.AccountingCoverage{DatabaseID: "database", Name: "orders", State: managedpostgres.StateDeleted, AccountingRequired: true, AccountingDatabaseID: "root"}, Blocking: true, Reasons: []string{"legacy_identity_unknown"}, RequiredFrom: at}}})
	d := view.Items[0]
	if d.RequiredUntil != nil || d.RequiredFrom == nil || d.RequiredFrom.Location() != time.UTC || !reflect.DeepEqual(d.Reasons, []string{"legacy_identity_unknown"}) {
		t.Fatalf("projection: %+v", d)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"provider_resource_id", "backend_fingerprint", "password", "connection_url", "lease_token"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("private field %s exposed", secret)
		}
	}
}
