// adr: 20261025094915 — compute overage stays hidden until its capability is
// released, is refused on Free, and doubles the compute ceiling while on.
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

func TestManagedPostgresOverageRoutes(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv := *e.S()
	service, _, _ := resizeAPIService(t, (*e.Acct()).ID)
	(*srv.ManagedPostgres()) = service
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, http.MethodGet, "/v1/postgres/overage", nil, nil); !missingRoute(r) {
		t.Fatalf("overage before its capability is released = %d %s, want a missing route", r.Code, r.Body)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "managed-postgres"), platformflags.Definition{Enabled: true, Default: true})
	if r := e.Do(t, http.MethodGet, "/v1/postgres/overage", nil, nil); !missingRoute(r) {
		t.Fatalf("overage with only its parent released = %d %s, want a missing route", r.Code, r.Body)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "postgres-overage"), platformflags.Definition{Enabled: true, Default: true})

	const allowance = int64(75 * 3600)
	read := func(t *testing.T, body string) api.ManagedPostgresOverage {
		t.Helper()
		var view api.ManagedPostgresOverage
		if err := json.Unmarshal([]byte(body), &view); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		return view
	}
	r := e.Do(t, http.MethodGet, "/v1/postgres/overage", nil, nil)
	if view := read(t, r.Body.String()); r.Code != http.StatusOK || view.Enabled || view.ComputeAllowanceUnitSeconds != allowance || view.ComputeCeilingUnitSeconds != allowance {
		t.Fatalf("default overage = %d %s, want off at the allowance", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodPut, "/v1/postgres/overage", map[string]any{}, nil); r.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled = %d %s, want 400", r.Code, r.Body)
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*e.Store()).CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "read only", []string{"postgres:read"}); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, http.MethodPut, "/v1/postgres/overage", map[string]any{"enabled": true}, map[string]string{"Authorization": "Bearer " + token}); r.Code != http.StatusForbidden {
		t.Fatalf("read scope turned overage on: %d %s", r.Code, r.Body)
	}
	r = e.Do(t, http.MethodPut, "/v1/postgres/overage", map[string]any{"enabled": true}, nil)
	if view := read(t, r.Body.String()); r.Code != http.StatusOK || !view.Enabled || view.ComputeCeilingUnitSeconds != 2*allowance {
		t.Fatalf("overage on = %d %s, want the ceiling at twice the allowance", r.Code, r.Body)
	}
	r = e.Do(t, http.MethodGet, "/v1/account/managed-postgres-usage", nil, nil)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"overage_enabled":true`) {
		t.Fatalf("usage with overage on = %d %s", r.Code, r.Body)
	}
	r = e.Do(t, http.MethodPut, "/v1/postgres/overage", map[string]any{"enabled": false}, nil)
	if view := read(t, r.Body.String()); r.Code != http.StatusOK || view.Enabled || view.ComputeCeilingUnitSeconds != allowance {
		t.Fatalf("overage off = %d %s", r.Code, r.Body)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "postgres-overage"), platformflags.Definition{Enabled: false})
	if r := e.Do(t, http.MethodGet, "/v1/postgres/overage", nil, nil); !missingRoute(r) {
		t.Fatalf("overage after its capability is withdrawn = %d %s", r.Code, r.Body)
	}
}

func TestManagedPostgresOverageIsRefusedOnFree(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	service, _, _ := resizeAPIService(t, (*e.Acct()).ID)
	(*(*e.S()).ManagedPostgres()) = service
	if r := e.Do(t, http.MethodPut, "/v1/postgres/overage", map[string]any{"enabled": true}, nil); r.Code != http.StatusForbidden || !strings.Contains(r.Body.String(), "managed_postgres_not_in_plan") {
		t.Fatalf("free overage = %d %s, want a plan refusal", r.Code, r.Body)
	}
	r := e.Do(t, http.MethodGet, "/v1/postgres/overage", nil, nil)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"enabled":false`) {
		t.Fatalf("free overage read = %d %s", r.Code, r.Body)
	}
}
