// adr: 20261029174950 — managed PostgreSQL delete grace period.
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// graceAPIProvider holds and deletes; the counts show what the API asked of
// the provider.
type graceAPIProvider struct {
	resizeAPIProvider
	holds, releases, deletes *int
}

func (p graceAPIProvider) HoldForDeletion(context.Context, string) error {
	*p.holds++
	return nil
}

func (p graceAPIProvider) ReleaseDeletionHold(context.Context, string) error {
	*p.releases++
	return nil
}

func (p graceAPIProvider) Delete(context.Context, managedpostgres.DeleteRequest) (managedpostgres.DeleteResult, error) {
	*p.deletes++
	return managedpostgres.DeleteResult{Done: true}, nil
}

func graceAPIService(t *testing.T, account string, spec managedpostgres.Spec) (*managedpostgres.Service, managedpostgres.Database, graceAPIProvider) {
	t.Helper()
	provider := graceAPIProvider{holds: new(int), releases: new(int), deletes: new(int)}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "us-east-1", MaxDatabasesPerAccount: 3, Defaults: map[string]string{"us-east-1": "private-backend"}, Backends: []managedpostgres.BackendConfig{{ID: "private-backend", Driver: "fixture", Region: "us-east-1", Namespace: "private"}}}, func(string) string { return "PRIVATE_SECRET" }, map[string]managedpostgres.Factory{"fixture": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
		return provider, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := managedpostgres.NewService(registry, managedpostgres.NewMemoryStore(), managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	d, err := svc.Create(t.Context(), managedpostgres.CreateRequest{AccountID: account, Name: "orders", Spec: spec})
	if err != nil || d.State != managedpostgres.StateReady {
		t.Fatal(d.State, err)
	}
	return svc, d, provider
}

func graceTestSpec(class managedpostgres.ServiceClass) managedpostgres.Spec {
	return managedpostgres.Spec{Region: "us-east-1", PostgresMajor: 17, Class: class, Availability: managedpostgres.AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600}
}

func decodeManagedPostgresDatabase(t *testing.T, body []byte) api.ManagedPostgresDatabase {
	t.Helper()
	var view api.ManagedPostgresDatabase
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err, string(body))
	}
	return view
}

func TestManagedPostgresDeleteKeepsTheDatabaseUntilUndeleteOrPurge(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	service, database, provider := graceAPIService(t, (*e.Acct()).ID, graceTestSpec(managedpostgres.ClassDevelopment))
	(*(*e.S()).ManagedPostgres()) = service
	path := "/v1/postgres/databases/" + database.ID

	before := time.Now()
	rec := e.Do(t, http.MethodDelete, path, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatal("delete", rec.Code, rec.Body.String())
	}
	view := decodeManagedPostgresDatabase(t, rec.Body.Bytes())
	until, err := time.Parse(time.RFC3339Nano, view.RestorableUntil)
	if view.State != "deleting" || err != nil || until.Before(before.Add(api.ManagedPostgresDeleteGracePeriod)) || until.After(time.Now().Add(api.ManagedPostgresDeleteGracePeriod)) {
		t.Fatalf("scheduled delete = %+v, %v", view, err)
	}
	if *provider.holds != 1 || *provider.deletes != 0 {
		t.Fatalf("holds=%d deletes=%d; want the database held, not deleted", *provider.holds, *provider.deletes)
	}
	if got := decodeManagedPostgresDatabase(t, e.Do(t, http.MethodGet, path, nil, nil).Body.Bytes()); got.RestorableUntil != view.RestorableUntil {
		t.Fatalf("read restorable_until = %q, want %q", got.RestorableUntil, view.RestorableUntil)
	}

	rec = e.Do(t, http.MethodPost, path+"/undelete", nil, nil)
	if view = decodeManagedPostgresDatabase(t, rec.Body.Bytes()); rec.Code != http.StatusOK || view.State != "ready" || view.RestorableUntil != "" || *provider.releases != 1 {
		t.Fatalf("undelete = %d %+v; releases=%d", rec.Code, view, *provider.releases)
	}
	if rec = e.Do(t, http.MethodPost, path+"/undelete", nil, nil); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "not scheduled for deletion") {
		t.Fatal("undelete of a ready database", rec.Code, rec.Body.String())
	}

	if rec = e.Do(t, http.MethodDelete, path+"?purge=soon", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatal("invalid purge", rec.Code, rec.Body.String())
	}
	if rec = e.Do(t, http.MethodDelete, path, nil, nil); rec.Code != http.StatusOK {
		t.Fatal("second schedule", rec.Code, rec.Body.String())
	}
	rec = e.Do(t, http.MethodDelete, path+"?purge=true", nil, nil)
	if view = decodeManagedPostgresDatabase(t, rec.Body.Bytes()); rec.Code != http.StatusOK || view.State != "deleted" || view.RestorableUntil != "" || *provider.deletes != 1 {
		t.Fatalf("purge = %d %+v; deletes=%d", rec.Code, view, *provider.deletes)
	}
	if rec = e.Do(t, http.MethodPost, path+"/undelete", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatal("undelete after purge", rec.Code, rec.Body.String())
	}
}

func TestManagedPostgresUndeleteChecksThePlan(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	// Hobby has no burstable class; the database was created under a larger plan.
	service, database, provider := graceAPIService(t, (*e.Acct()).ID, graceTestSpec(managedpostgres.ClassBurstable))
	(*(*e.S()).ManagedPostgres()) = service
	if _, err := service.ScheduleDelete(t.Context(), database.AccountID, database.ID); err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodPost, "/v1/postgres/databases/"+database.ID+"/undelete", nil, nil)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "managed_postgres_not_in_plan") {
		t.Fatal("undelete outside the plan", rec.Code, rec.Body.String())
	}
	if got, err := service.Get(t.Context(), database.AccountID, database.ID); err != nil || got.State != managedpostgres.StateDeleting || *provider.releases != 0 {
		t.Fatalf("refused undelete changed the database: %+v %v releases=%d", got, err, *provider.releases)
	}
}
