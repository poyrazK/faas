// adr: 20261106103654 — a restore without point_in_time branches from the
// latest point, and ttl_seconds sets the branch's expires_at.
package apidserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// branchAPIProvider restores into a ready copy at once.
type branchAPIProvider struct{ resizeAPIProvider }

func (branchAPIProvider) Capabilities() managedpostgres.Capabilities {
	c := resizeAPIProvider{}.Capabilities()
	c.PointInTimeRestore = true
	c.RestoreUsageIsolated = true
	return c
}

func (branchAPIProvider) Restore(_ context.Context, r managedpostgres.RestoreRequest) (managedpostgres.ObservedDatabase, error) {
	return managedpostgres.ObservedDatabase{ProviderResourceID: "PRIVATE_BRANCH_" + r.ResourceID, DataResourceID: "PRIVATE_BRANCH_DATA_" + r.ResourceID, Spec: r.Spec, Status: managedpostgres.ProviderStatusReady,
		RestoreLineage: &managedpostgres.RestoreLineage{SourceResourceID: r.SourceResourceID, PointInTime: r.PointInTime}}, nil
}

func branchAPIService(t *testing.T, account string, now *time.Time) (*managedpostgres.Service, managedpostgres.Database) {
	t.Helper()
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "us-east-1", MaxDatabasesPerAccount: 3, Defaults: map[string]string{"us-east-1": "private-backend"}, Backends: []managedpostgres.BackendConfig{{ID: "private-backend", Driver: "fixture", Region: "us-east-1", Namespace: "private"}}}, func(string) string { return "PRIVATE_SECRET" }, map[string]managedpostgres.Factory{"fixture": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
		return branchAPIProvider{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := managedpostgres.NewService(registry, managedpostgres.NewMemoryStore(), managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }, Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	source, err := svc.Create(t.Context(), managedpostgres.CreateRequest{AccountID: account, Name: "orders", Spec: graceTestSpec(managedpostgres.ClassDevelopment)})
	if err != nil || source.State != managedpostgres.StateReady {
		t.Fatal(source.State, err)
	}
	return svc, source
}

func TestManagedPostgresRestoreWithoutAPointBranchesFromLatestWithATTL(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	service, source := branchAPIService(t, (*e.Acct()).ID, &now)
	(*(*e.S()).ManagedPostgres()) = service
	now = now.Add(time.Minute)
	path := "/v1/postgres/databases/" + source.ID + "/restore"

	rec := e.Do(t, http.MethodPost, path, api.RestoreManagedPostgresDatabaseRequest{Name: "pr-42", TTLSeconds: 7200}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("branch = %d %s", rec.Code, rec.Body.String())
	}
	branch := decodeManagedPostgresDatabase(t, rec.Body.Bytes())
	if branch.RestoreSourceDatabaseID != source.ID || branch.RestorePointInTime != now.Add(-managedpostgres.LatestRestoreLag).Format(time.RFC3339Nano) {
		t.Fatalf("branch lineage = %q at %q", branch.RestoreSourceDatabaseID, branch.RestorePointInTime)
	}
	if branch.ExpiresAt != now.Add(2*time.Hour).Format(time.RFC3339Nano) {
		t.Fatalf("expires_at = %q, want two hours after creation", branch.ExpiresAt)
	}
	if strings.Contains(rec.Body.String(), "PRIVATE_") {
		t.Fatalf("the view leaked provider identities: %s", rec.Body.String())
	}

	for name, body := range map[string]api.RestoreManagedPostgresDatabaseRequest{
		"ttl under the floor": {Name: "pr-low", TTLSeconds: api.ManagedPostgresBranchTTLMinSeconds - 1},
		"ttl over the cap":    {Name: "pr-high", TTLSeconds: api.ManagedPostgresBranchTTLMaxSeconds + 1},
		"unparseable point":   {Name: "pr-bad", PointInTime: "yesterday"},
	} {
		if rec := e.Do(t, http.MethodPost, path, body, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, rec.Code, rec.Body.String())
		}
	}

	// An explicit point still restores exactly there, with no expiry.
	point := now.Add(-30 * time.Second).Format(time.RFC3339Nano)
	rec = e.Do(t, http.MethodPost, path, api.RestoreManagedPostgresDatabaseRequest{Name: "orders-before", PointInTime: point}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("restore at a point = %d %s", rec.Code, rec.Body.String())
	}
	if restored := decodeManagedPostgresDatabase(t, rec.Body.Bytes()); restored.RestorePointInTime != point || restored.ExpiresAt != "" {
		t.Fatalf("restore at a point = %q, expires %q", restored.RestorePointInTime, restored.ExpiresAt)
	}
}
