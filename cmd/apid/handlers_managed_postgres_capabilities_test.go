// adr: 592 — portable reader permissions and capability discovery.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Provider mutations and reads panic through the nil embedded interface.
type capabilityOnlyProvider struct{ managedpostgres.Provider }

func (capabilityOnlyProvider) Capabilities() managedpostgres.Capabilities {
	return managedpostgres.Capabilities{
		PostgresMajors: []int{17, 16}, ServiceClasses: []managedpostgres.ServiceClass{managedpostgres.ClassProduction, managedpostgres.ClassDevelopment, managedpostgres.ClassBurstable},
		Availability:     []managedpostgres.Availability{managedpostgres.AvailabilitySingleZone},
		CredentialAccess: []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialReadOnly, managedpostgres.CredentialMigration},
		ScaleToZero:      true, PooledConnections: true, PointInTimeRestore: true, RestorePreflight: true, RestoreUsageIsolated: true,
		MaxStorageBytes: 1 << 30, MaxRestoreWindowSeconds: 3600, UsageMeters: []managedpostgres.Meter{managedpostgres.MeterComputeUnitSeconds},
	}
}

func capabilitiesService(t *testing.T, canary string) *managedpostgres.Service {
	t.Helper()
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "us-east-1", MaxDatabasesPerAccount: 2,
		Defaults: map[string]string{"us-east-1": "private-backend"}, Backends: []managedpostgres.BackendConfig{{ID: "private-backend", Driver: "fixture", Region: "us-east-1", Namespace: "private-namespace"}}},
		func(string) string { return "private-secret" }, map[string]managedpostgres.Factory{"fixture": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return capabilityOnlyProvider{}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := managedpostgres.NewService(registry, managedpostgres.NewMemoryStore(), managedpostgres.ServiceOptions{
		ProvisioningEnabled: func() bool { return true }, ProvisioningAllowed: func(_ context.Context, account string) bool { return account == canary },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestManagedPostgresCapabilitiesRoute(t *testing.T) {
	e := setup(t, api.PlanHobby)
	e.s.managedPostgres = capabilitiesService(t, e.acct.ID)
	path := "/v1/postgres/capabilities"
	response := e.do(t, http.MethodGet, path, nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("capabilities: %d %s", response.Code, response.Body)
	}
	var out api.ManagedPostgresCapabilities
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	limits, _ := api.ManagedPostgresLimitsFor(api.PlanHobby)
	if out.ContractVersion != managedpostgres.CapabilityContractVersion || out.Region != "us-east-1" || !out.ProvisioningEnabled || out.DatabaseLimit != min(2, limits.DatabasesMax) ||
		!slices.Equal(out.ServiceClasses, []string{"development"}) || !slices.Equal(out.PostgresMajors, []int{16, 17}) ||
		!slices.Contains(out.CredentialAccess, "read_only") || out.StorageLimitBytes != 1<<30 || out.RestoreWindowSeconds != 3600 || !out.RestorePreflight || !out.PointInTimeRestore || out.AlwaysOn {
		t.Fatalf("effective capabilities=%+v", out)
	}
	for _, secret := range []string{"private-backend", "private-namespace", "private-secret", "fingerprint", "provider"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("capability response leaked placement details")
		}
	}
	e.s.managedPostgres = capabilitiesService(t, "another-canary")
	response = e.do(t, http.MethodGet, path+"?region=us-east-1", nil, nil)
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil || out.ProvisioningEnabled || !slices.Contains(out.CredentialAccess, "read_only") {
		t.Fatal("closed rollout hid configured support", err)
	}
	for _, query := range []string{"?unknown=value", "?region=us-east-1&region=us-east-1", "?region=%ZZ", "?region=x;y", "?region=" + strings.Repeat("a", 256)} {
		if rec := e.do(t, http.MethodGet, path+query, nil, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid query %q: %d %s", query, rec.Code, rec.Body)
		}
	}
	if rec := e.do(t, http.MethodGet, path+"?region=unconfigured", nil, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown region: %d %s", rec.Code, rec.Body)
	}
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response = httptest.NewRecorder()
	e.h.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated discovery: %d", response.Code)
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "wrong-scope", []string{"usage:read"}); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + plain}); rec.Code != http.StatusForbidden {
		t.Fatalf("discovery without postgres read scope: %d %s", rec.Code, rec.Body)
	}
}

func TestManagedPostgresCapabilitiesPlanAndAvailabilityLimits(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		t.Run(string(plan), func(t *testing.T) {
			limits, _ := api.ManagedPostgresLimitsFor(plan)
			d := managedpostgres.CapabilityDiscovery{Region: "us-east-1", Capabilities: capabilityOnlyProvider{}.Capabilities(), DatabaseLimit: 2, ProvisioningEnabled: true}
			out := managedPostgresCapabilitiesView(d, limits)
			if out.DatabaseLimit != min(2, limits.DatabasesMax) || (plan == api.PlanFree && (out.ProvisioningEnabled || len(out.CredentialAccess) != 0)) {
				t.Fatalf("plan capabilities=%+v", out)
			}
			for _, class := range out.ServiceClasses {
				if !managedPostgresPlanAllows(limits, managedpostgres.Spec{Class: managedpostgres.ServiceClass(class)}) {
					t.Fatal("unsupported plan class advertised", class)
				}
			}
			d.Capabilities.ScaleToZero = false
			out = managedPostgresCapabilitiesView(d, limits)
			if !limits.AlwaysOnAllowed && (out.ProvisioningEnabled || len(out.ServiceClasses) != 0) {
				t.Fatal("always-on-only backend advertised to restricted plan")
			}
		})
	}
}
