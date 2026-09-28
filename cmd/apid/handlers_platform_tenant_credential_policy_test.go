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

func TestPlatformTenantCredentialPolicyAPI(t *testing.T) {
	e := setup(t, api.PlanPro)
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID,
		"credential-policy-customer", "Credential policy customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/account/platform-tenants/" + tenant.ID + "/credential-policy"
	read := func() api.PlatformTenantCredentialPolicyResponse {
		t.Helper()
		resp := e.do(t, http.MethodGet, path, nil, nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("get policy: %d %s", resp.Code, resp.Body.String())
		}
		var policy api.PlatformTenantCredentialPolicyResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &policy); err != nil {
			t.Fatal(err)
		}
		return policy
	}
	if got := read(); got.Enabled || got.TenantID != tenant.ID || len(got.AllowedScopes) != 0 {
		t.Fatalf("default policy=%+v", got)
	}
	limit := 4
	input := api.SetPlatformTenantCredentialPolicyRequest{AllowedScopes: []string{"write", "read"}, MaxKeysPerConsumer: &limit}
	put := e.do(t, http.MethodPut, path, input, nil)
	if put.Code != http.StatusOK {
		t.Fatalf("put policy: %d %s", put.Code, put.Body.String())
	}
	first := read()
	if !first.Enabled || first.MaxKeysPerConsumer != limit || first.UpdatedAt == nil ||
		!reflect.DeepEqual(first.AllowedScopes, []string{"read", "write"}) {
		t.Fatalf("configured policy=%+v", first)
	}
	if replay := e.do(t, http.MethodPut, path, input, nil); replay.Code != http.StatusOK {
		t.Fatalf("idempotent put: %d %s", replay.Code, replay.Body.String())
	}
	if got := read(); !got.UpdatedAt.Equal(*first.UpdatedAt) {
		t.Fatalf("no-op policy replay changed updated_at: %s -> %s", first.UpdatedAt, got.UpdatedAt)
	}
	for _, invalid := range []api.SetPlatformTenantCredentialPolicyRequest{
		{AllowedScopes: []string{"secrets:write"}, MaxKeysPerConsumer: &limit},
		{AllowedScopes: []string{"read", "read"}, MaxKeysPerConsumer: &limit},
		{AllowedScopes: []string{}, MaxKeysPerConsumer: &limit},
		{AllowedScopes: []string{"read"}, MaxKeysPerConsumer: ptrInt(0)},
		{AllowedScopes: []string{"read"}, MaxKeysPerConsumer: ptrInt(api.MaxPlatformTenantKeysPerConsumer + 1)},
	} {
		if resp := e.do(t, http.MethodPut, path, invalid, nil); resp.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid policy status=%d body=%s", resp.Code, resp.Body.String())
		}
	}
	if resp := e.do(t, http.MethodGet, "/v1/account/platform-tenants/"+uuid.NewString()+"/credential-policy", nil, nil); resp.Code != http.StatusNotFound {
		t.Fatalf("unknown tenant status=%d body=%s", resp.Code, resp.Body.String())
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		resp := e.do(t, method, "/v1/account/platform-tenants/not-a-uuid/credential-policy", nil, nil)
		if resp.Code != http.StatusNotFound {
			t.Fatalf("malformed tenant id %s status=%d body=%s", method, resp.Code, resp.Body.String())
		}
	}
}
