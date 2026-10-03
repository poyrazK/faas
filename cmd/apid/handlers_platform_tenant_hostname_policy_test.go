package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPlatformTenantHostnamePolicyAPI(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e := setup(t, api.PlanPro)
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID,
		"hostname-policy-customer", "Hostname policy customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/account/platform-tenants/" + tenant.ID + "/hostname-policy"
	read := func() api.PlatformTenantHostnamePolicyResponse {
		t.Helper()
		resp := e.do(t, http.MethodGet, path, nil, nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("get policy: %d %s", resp.Code, resp.Body.String())
		}
		var policy api.PlatformTenantHostnamePolicyResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &policy); err != nil {
			t.Fatal(err)
		}
		return policy
	}
	if got := read(); got.Enabled || got.TenantID != tenant.ID || len(got.AllowedSuffixes) != 0 {
		t.Fatalf("default policy=%+v", got)
	}
	limit := 9
	input := api.SetPlatformTenantHostnamePolicyRequest{
		AllowedSuffixes: []string{" Vanity.Example.NET ", "customers.example.com"}, MaxHostnames: &limit,
	}
	put := e.do(t, http.MethodPut, path, input, nil)
	if put.Code != http.StatusOK {
		t.Fatalf("put policy: %d %s", put.Code, put.Body.String())
	}
	first := read()
	if !first.Enabled || first.MaxHostnames != limit || first.UpdatedAt == nil ||
		!reflect.DeepEqual(first.AllowedSuffixes, []string{"customers.example.com", "vanity.example.net"}) {
		t.Fatalf("configured policy=%+v", first)
	}
	if replay := e.do(t, http.MethodPut, path, input, nil); replay.Code != http.StatusOK {
		t.Fatalf("idempotent put: %d %s", replay.Code, replay.Body.String())
	}
	if got := read(); !got.UpdatedAt.Equal(*first.UpdatedAt) {
		t.Fatalf("no-op policy replay changed updated_at: %s -> %s", first.UpdatedAt, got.UpdatedAt)
	}
	for _, invalid := range []api.SetPlatformTenantHostnamePolicyRequest{
		{AllowedSuffixes: []string{"*.example.com"}, MaxHostnames: &limit},
		{AllowedSuffixes: []string{"example.com", " EXAMPLE.com "}, MaxHostnames: &limit},
		{AllowedSuffixes: []string{}, MaxHostnames: &limit},
		{AllowedSuffixes: []string{"example.com"}, MaxHostnames: ptrInt(0)},
		{AllowedSuffixes: []string{"example.com"}, MaxHostnames: ptrInt(api.MaxPlatformTenantDelegatedHosts + 1)},
	} {
		if resp := e.do(t, http.MethodPut, path, invalid, nil); resp.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid policy status=%d body=%s", resp.Code, resp.Body.String())
		}
	}
	if resp := e.do(t, http.MethodGet, "/v1/account/platform-tenants/"+uuid.NewString()+"/hostname-policy", nil, nil); resp.Code != http.StatusNotFound {
		t.Fatalf("unknown tenant status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func ptrInt(v int) *int { return &v }
