// adr: 582 — diagnostics are local, operator-only, and do not reveal provider IDs.
package apidserver_test

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
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// Any provider call panics through the nil embedded interface.
type diagnosticsNoProvider struct{ managedpostgres.Provider }

func (diagnosticsNoProvider) Capabilities() managedpostgres.Capabilities {
	return managedpostgres.Capabilities{
		PostgresMajors: []int{16}, ServiceClasses: []managedpostgres.ServiceClass{managedpostgres.ClassDevelopment}, Availability: []managedpostgres.Availability{managedpostgres.AvailabilitySingleZone}, CredentialAccess: []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite}, UsageMeters: []managedpostgres.Meter{managedpostgres.MeterComputeUnitSeconds}}
}

type diagnosticsAccountStore struct {
	Store   state.Store
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
	e := apidserver.Setup(t, api.PlanPro)
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
	(*(*e.S()).ManagedPostgres()) = service
	target := (*e.Acct())
	target.ID = uuid.NewString()
	catalog := &diagnosticsAccountStore{Store: (*e.Store()), target: target}
	(*(*e.S()).Store()) = statetest.NewStore(catalog)
	path := "/v1/admin/managed-postgres/accounting/" + target.ID
	if rec := e.Do(t, http.MethodGet, path, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("nonoperator: %d %s", rec.Code, rec.Body)
	}
	if catalog.lookups != 0 {
		t.Fatal("denied operator resolved tenant")
	}
	(*e.S()).WithAdminAllowlist((*e.Acct()).Email)
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateAPIKey(context.Background(), (*e.Acct()).ID, hash, "usage-only", []string{"usage:read"}); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + plain}); rec.Code != http.StatusForbidden {
		t.Fatalf("missing admin scope: %d %s", rec.Code, rec.Body)
	}
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=x", "?after=bad"} {
		if rec := e.Do(t, http.MethodGet, path+query, nil, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid query: %d %s", rec.Code, rec.Body)
		}
	}
	if catalog.lookups != 0 {
		t.Fatal("invalid pagination resolved tenant")
	}
	rec := e.Do(t, http.MethodGet, path+"?limit=2", nil, nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("report: %d %s", rec.Code, rec.Body)
	}
	var page api.ManagedPostgresAccountingDiagnosticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || page.AccountID != target.ID || page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("report: %+v %v", page, err)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/admin/managed-postgres/accounting/"+uuid.NewString(), nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing account: %d %s", rec.Code, rec.Body)
	}
	if _, err := (*e.Store()).SetMFARequired(context.Background(), (*e.Acct()).ID, true); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	sid := uuid.NewString()
	if _, err := (*e.Store()).CreateSession(req.Context(), sid, (*e.Acct()).ID, "192.0.2.10", "test"); err != nil {
		t.Fatal(err)
	}
	token, err := (*(*e.S()).Sessions()).IssueWithSessionAndBindingHashAndStepUp(sid, (*e.Acct()).ID, "", time.Time{}, true)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: token})
	rec = httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeMFARequired) {
		t.Fatalf("MFA missing: %d %s", rec.Code, rec.Body)
	}
}

func TestManagedPostgresAccountingDiagnosticsProjection(t *testing.T) {
	at := time.Date(2026, 10, 5, 12, 0, 0, 0, time.FixedZone("local", 3*3600))
	view := apidserver.ManagedPostgresAccountingDiagnosticsView(managedpostgres.AccountingDiagnostics{AccountID: "account", EvaluatedAt: at, PolicyEnabled: true, Window: time.Hour, Items: []managedpostgres.AccountingDiagnostic{{AccountingCoverage: managedpostgres.AccountingCoverage{DatabaseID: "database", Name: "orders", State: managedpostgres.StateDeleted, AccountingRequired: true, AccountingDatabaseID: "root"}, Blocking: true, Reasons: []string{"legacy_identity_unknown"}, RequiredFrom: at}}})
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
