// adr: 463 — cached, tenant-scoped provider health in customer responses.

package main

import (
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

func TestManagedPostgresHealthGetListAndAccountIsolation(t *testing.T) {
	env := newSourceRefTestServer(t, api.PlanPro, "health", 9003)
	store, _, databaseID := configureSourceRefManagedPostgres(t, env)
	ctx := context.Background()
	acct, err := env.store.AccountByID(ctx, env.acctID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claim, err := store.ClaimHealthCheck(ctx, "health", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	result := managedpostgres.HealthResult{HealthSnapshot: managedpostgres.HealthSnapshot{
		ProviderStatus: "ready", ComputeState: managedpostgres.ComputeStateSuspended, CheckedAt: now,
	}, Succeeded: true, NextCheckAt: now.Add(time.Minute)}
	if err := store.FinishHealthCheck(ctx, claim, result); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/postgres/databases/"+databaseID, nil)
	request.SetPathValue("id", databaseID)
	get := httptest.NewRecorder()
	env.srv.getManagedPostgresDatabase(get, request, acct)
	var database api.ManagedPostgresDatabase
	if err := json.Unmarshal(get.Body.Bytes(), &database); err != nil || get.Code != http.StatusOK {
		t.Fatalf("get: %d %s %v", get.Code, get.Body, err)
	}
	if database.State != "ready" || database.Health == nil || database.Health.Status != "healthy" || !database.Health.Fresh || database.Health.ComputeState != "suspended" || database.Health.LastSuccessAt == "" {
		t.Fatalf("health: %+v", database.Health)
	}
	list := httptest.NewRecorder()
	env.srv.listManagedPostgresDatabases(list, request, acct)
	var databases api.ManagedPostgresDatabaseList
	if err := json.Unmarshal(list.Body.Bytes(), &databases); err != nil || list.Code != http.StatusOK || len(databases.Items) != 1 || databases.Items[0].Health.Status != "healthy" {
		t.Fatalf("list: %d %s %v", list.Code, list.Body, err)
	}
	for _, body := range []string{get.Body.String(), list.Body.String()} {
		for _, private := range []string{claim.Database.ProviderResourceID, claim.Database.BackendFingerprint, "connection_url", "password"} {
			if strings.Contains(body, private) {
				t.Fatalf("response exposed private provider material: %s", body)
			}
		}
	}
	acct.ID = uuid.NewString()
	otherGet := httptest.NewRecorder()
	env.srv.getManagedPostgresDatabase(otherGet, request, acct)
	if otherGet.Code != http.StatusNotFound {
		t.Fatalf("cross-account get: %d %s", otherGet.Code, otherGet.Body)
	}
	otherList := httptest.NewRecorder()
	env.srv.listManagedPostgresDatabases(otherList, request, acct)
	if err := json.Unmarshal(otherList.Body.Bytes(), &databases); err != nil || len(databases.Items) != 0 {
		t.Fatalf("cross-account list: %s %v", otherList.Body, err)
	}
}

func TestManagedPostgresHealthViewRetainsFailureAndStaleness(t *testing.T) {
	now := time.Now().UTC()
	policy := managedpostgres.HealthPolicy{Enabled: true, Interval: time.Minute, StaleAfter: 5 * time.Minute}
	snapshot := managedpostgres.HealthSnapshot{ProviderStatus: "missing", ComputeState: managedpostgres.ComputeStateUnknown,
		CheckedAt: now, LastSuccessAt: now.Add(-time.Hour), LastErrorCode: "resource_missing"}
	for _, tc := range []struct {
		age    time.Duration
		status string
		fresh  bool
	}{
		{0, "degraded", true}, {6 * time.Minute, "stale", false},
	} {
		health := policy.Summarize(snapshot, now.Add(tc.age))
		view := managedPostgresView(managedpostgres.Database{State: managedpostgres.StateReady, Health: &health})
		if view.State != "ready" || view.Health.Status != tc.status || view.Health.Fresh != tc.fresh || view.Health.LastErrorCode != "resource_missing" || view.Health.LastSuccessAt != snapshot.LastSuccessAt.Format(time.RFC3339Nano) {
			t.Fatalf("view: %+v", view.Health)
		}
	}
}
