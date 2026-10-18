// adr: 1027 — in-place major upgrades are invisible until the owner lists
// an account: outside FAAS_MANAGED_POSTGRES_UPGRADE_ACCOUNTS their routes
// answer exactly like unregistered ones, a method mismatch included.
package apidserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/managedpg"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type upgradeAPIProvider struct{ resizeAPIProvider }

func (upgradeAPIProvider) Capabilities() managedpostgres.Capabilities {
	c := resizeAPIProvider{}.Capabilities()
	c.PostgresMajors, c.MajorUpgrade = []int{16, 17, 18}, true
	return c
}

func TestManagedPostgresUpgradeHiddenOutsideTheRollout(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	account := (*e.Acct()).ID
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "us-east-1", MaxDatabasesPerAccount: 3, Defaults: map[string]string{"us-east-1": "private-backend"},
		Backends: []managedpostgres.BackendConfig{{ID: "private-backend", Driver: "fixture", Region: "us-east-1", Namespace: "private"}}}, func(string) string { return "" },
		map[string]managedpostgres.Factory{"fixture": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) { return upgradeAPIProvider{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	service, err := managedpostgres.NewService(registry, managedpostgres.NewMemoryStore(), managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	d, err := service.Create(context.Background(), managedpostgres.CreateRequest{AccountID: account, Name: "orders", Spec: managedpostgres.Spec{Region: "us-east-1", PostgresMajor: 17,
		Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	(*(*e.S()).ManagedPostgres()) = service
	id := uuid.NewString()
	path := "/v1/postgres/databases/" + d.ID + "/upgrade"
	progress := "/v1/postgres/databases/" + d.ID + "/upgrades/" + id
	payload := map[string]any{"request_id": id, "postgres_major": 18}
	missing := e.Do(t, http.MethodPost, "/v1/postgres/databases/"+d.ID+"/no-such-route", payload, nil)
	hidden := func(name string, method, target string, headers map[string]string) {
		t.Helper()
		rec := e.Do(t, method, target, payload, headers)
		if rec.Code != missing.Code || rec.Body.String() != missing.Body.String() || rec.Header().Get("Allow") != "" {
			t.Errorf("%s: %d %q, want what a missing route answers: %d %q", name, rec.Code, rec.Body.String(), missing.Code, missing.Body.String())
		}
	}

	// Nobody listed: hidden for everyone, before authentication.
	hidden("unlisted POST", http.MethodPost, path, nil)
	hidden("unlisted GET", http.MethodGet, progress, nil)
	hidden("anonymous", http.MethodPost, path, map[string]string{"Authorization": ""})
	hidden("method mismatch", http.MethodPut, path, nil)

	// Another account listed: still hidden for this one.
	rollouts := (*e.S()).PostgresUpgrades()
	if *rollouts, err = managedpg.ParseRollout("TEST", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	hidden("another account listed", http.MethodPost, path, nil)
	hidden("method mismatch while previewing", http.MethodPut, path, nil)

	// Listed: the upgrade is journaled and reported.
	if *rollouts, err = managedpg.ParseRollout("TEST", account); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodPost, path, payload, nil); rec.Code != http.StatusAccepted {
		t.Fatalf("listed POST = %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.Do(t, http.MethodGet, progress, nil, nil); rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("listed GET = %d %s", rec.Code, rec.Body.String())
	}
	// Capabilities never mention upgrades before launch.
	if rec := e.Do(t, http.MethodGet, "/v1/postgres/capabilities", nil, nil); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "upgrade") {
		t.Fatalf("capabilities = %d %s", rec.Code, rec.Body.String())
	}
}
