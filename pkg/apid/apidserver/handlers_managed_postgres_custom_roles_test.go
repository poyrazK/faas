// adr: 20261116213000 — custom Postgres group roles.
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

type customRolesAPIProvider struct {
	resizeAPIProvider
	applied *[][]managedpostgres.CustomRoleSpec
}

func (p customRolesAPIProvider) ApplyCustomRoles(_ context.Context, _ string, desired []managedpostgres.CustomRoleSpec) error {
	*p.applied = append(*p.applied, desired)
	return nil
}

type customRolesAPISink struct{}

func (customRolesAPISink) Put(context.Context, managedpostgres.Binding, managedpostgres.CredentialMaterial) (string, error) {
	return "ref", nil
}
func (customRolesAPISink) Delete(context.Context, managedpostgres.Binding) error { return nil }

// installCustomRoles wires a database service and binding service whose
// provider records custom-role declarations.
func installCustomRoles(t *testing.T, accountID string, services **managedpostgres.Service, bindingServices **managedpostgres.BindingService) (managedpostgres.Database, *[][]managedpostgres.CustomRoleSpec) {
	t.Helper()
	applied := &[][]managedpostgres.CustomRoleSpec{}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "us-east-1", MaxDatabasesPerAccount: 3, Defaults: map[string]string{"us-east-1": "private-backend"},
		Backends: []managedpostgres.BackendConfig{{ID: "private-backend", Driver: "fixture", Region: "us-east-1", Namespace: "private"}}},
		func(string) string { return "PRIVATE_SECRET" }, map[string]managedpostgres.Factory{"fixture": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return customRolesAPIProvider{applied: applied}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	store := managedpostgres.NewMemoryStore()
	service, err := managedpostgres.NewService(registry, store, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	d, err := service.Create(t.Context(), managedpostgres.CreateRequest{AccountID: accountID, Name: "windmill-db", Spec: managedpostgres.Spec{Region: "us-east-1", PostgresMajor: 17,
		Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := managedpostgres.NewBindingService(registry, store, store, customRolesAPISink{}, managedpostgres.BindingServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	*services = service
	*bindingServices = bindings
	return d, applied
}

func TestManagedPostgresCustomRoleRoutes(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	d, applied := installCustomRoles(t, (*e.Acct()).ID, (*e.S()).ManagedPostgres(), (*e.S()).ManagedPostgresBindings())
	base := "/v1/postgres/databases/" + d.ID + "/roles"

	// Beta boots the flag on (ADR-20261125232048); an operator turning it
	// off darkens the routes.
	(*e.S()).SeedPlatformFlag(platformflags.PostgresCustomRoles, platformflags.Definition{})
	if rec := e.Do(t, http.MethodGet, base, nil, nil); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "postgres_custom_roles_unavailable") {
		t.Fatalf("unreleased: %d %s", rec.Code, rec.Body.String())
	}
	(*e.S()).EnablePlatformFlagForAll(platformflags.PostgresCustomRoles)

	rec := e.Do(t, http.MethodPut, base+"/windmill_user", api.PutManagedPostgresCustomRoleRequest{GrantedTo: []string{"migration", "read_write"}}, nil)
	var role api.ManagedPostgresCustomRole
	if err := json.Unmarshal(rec.Body.Bytes(), &role); err != nil || rec.Code != http.StatusOK || role.State != "ready" || len(role.GrantedTo) != 2 {
		t.Fatalf("put user: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.Do(t, http.MethodPut, base+"/windmill_admin", api.PutManagedPostgresCustomRoleRequest{BypassRLS: true, GrantedTo: []string{"migration", "read_write"}, MemberOf: []string{"windmill_user"}}, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"bypass_rls":true`) {
		t.Fatalf("put admin: %d %s", rec.Code, rec.Body.String())
	}
	if last := (*applied)[len(*applied)-1]; len(last) != 2 || last[1].Name != "windmill_admin" || !last[1].BypassRLS {
		t.Fatalf("provider declaration: %+v", last)
	}
	for _, name := range []string{"gregale_admin", "Admin", "pg_monitor"} {
		if rec := e.Do(t, http.MethodPut, base+"/"+name, api.PutManagedPostgresCustomRoleRequest{}, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("reserved name %s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := e.Do(t, http.MethodPut, base+"/api_role", api.PutManagedPostgresCustomRoleRequest{GrantedTo: []string{"data_api"}}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("data api grant: %d %s", rec.Code, rec.Body.String())
	}

	// A read-only key lists but cannot change roles.
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*e.Store()).CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "read only", []string{"postgres:read"}); err != nil {
		t.Fatal(err)
	}
	readOnly := map[string]string{"Authorization": "Bearer " + token}
	rec = e.Do(t, http.MethodGet, base, nil, readOnly)
	var list api.ManagedPostgresCustomRoleList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != http.StatusOK || len(list.Items) != 2 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.Do(t, http.MethodDelete, base+"/windmill_admin", nil, readOnly); rec.Code != http.StatusForbidden {
		t.Fatalf("read scope deleted: %d", rec.Code)
	}

	if rec := e.Do(t, http.MethodDelete, base+"/windmill_user", nil, nil); rec.Code != http.StatusConflict {
		t.Fatalf("delete parent: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.Do(t, http.MethodDelete, base+"/windmill_admin", nil, nil)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"state":"deleted"`) {
		t.Fatalf("delete admin: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.Do(t, http.MethodGet, "/v1/postgres/databases/00000000-0000-0000-0000-000000000000/roles", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown database: %d %s", rec.Code, rec.Body.String())
	}
}

func TestManagedPostgresCustomRolesNeedHobby(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	d, _ := installCustomRoles(t, (*e.Acct()).ID, (*e.S()).ManagedPostgres(), (*e.S()).ManagedPostgresBindings())
	(*e.S()).EnablePlatformFlagForAll(platformflags.PostgresCustomRoles)
	rec := e.Do(t, http.MethodPut, "/v1/postgres/databases/"+d.ID+"/roles/windmill_user", api.PutManagedPostgresCustomRoleRequest{}, nil)
	if rec.Code != http.StatusPaymentRequired || !strings.Contains(rec.Body.String(), "plan_feature_gated") {
		t.Fatalf("free plan: %d %s", rec.Code, rec.Body.String())
	}
}
