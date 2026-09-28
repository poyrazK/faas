package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPlatformTenantConsumerProvisioningPolicyAPI(t *testing.T) {
	e := setup(t, api.PlanPro)
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID,
		"consumer-provisioning", "Consumer provisioning", 250)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/account/platform-tenants/" + tenant.ID + "/consumer-provisioning-policy"
	read := func() api.PlatformTenantConsumerProvisioningPolicyResponse {
		t.Helper()
		resp := e.do(t, http.MethodGet, path, nil, nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("get policy: %d %s", resp.Code, resp.Body.String())
		}
		var policy api.PlatformTenantConsumerProvisioningPolicyResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &policy); err != nil {
			t.Fatal(err)
		}
		return policy
	}
	if got := read(); got.Enabled || got.TenantID != tenant.ID || got.MaxConsumers != 0 {
		t.Fatalf("default policy=%+v", got)
	}
	enabled := true
	limit := 1000
	input := api.SetPlatformTenantConsumerProvisioningPolicyRequest{Enabled: &enabled, MaxConsumers: &limit}
	put := e.do(t, http.MethodPut, path, input, nil)
	if put.Code != http.StatusOK {
		t.Fatalf("put policy: %d %s", put.Code, put.Body.String())
	}
	first := read()
	if !first.Enabled || first.MaxConsumers != limit || first.UpdatedAt == nil {
		t.Fatalf("configured policy=%+v", first)
	}
	if replay := e.do(t, http.MethodPut, path, input, nil); replay.Code != http.StatusOK {
		t.Fatalf("idempotent put: %d %s", replay.Code, replay.Body.String())
	}
	if got := read(); !got.UpdatedAt.Equal(*first.UpdatedAt) {
		t.Fatalf("no-op replay changed updated_at: %s -> %s", first.UpdatedAt, got.UpdatedAt)
	}
	disabled := false
	disable := api.SetPlatformTenantConsumerProvisioningPolicyRequest{Enabled: &disabled, MaxConsumers: ptrInt(0)}
	if resp := e.do(t, http.MethodPut, path, disable, nil); resp.Code != http.StatusOK {
		t.Fatalf("disable policy: %d %s", resp.Code, resp.Body.String())
	}
	if got := read(); got.Enabled || got.MaxConsumers != 0 {
		t.Fatalf("disabled policy=%+v", got)
	}
	for _, invalid := range []api.SetPlatformTenantConsumerProvisioningPolicyRequest{
		{},
		{Enabled: &enabled, MaxConsumers: ptrInt(0)},
		{Enabled: &disabled, MaxConsumers: ptrInt(1)},
		{Enabled: &enabled, MaxConsumers: ptrInt(api.MaxPlatformTenantSelfServiceConsumers + 1)},
	} {
		if resp := e.do(t, http.MethodPut, path, invalid, nil); resp.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid policy status=%d body=%s", resp.Code, resp.Body.String())
		}
	}
	if resp := e.do(t, http.MethodGet, "/v1/account/platform-tenants/"+uuid.NewString()+"/consumer-provisioning-policy", nil, nil); resp.Code != http.StatusNotFound {
		t.Fatalf("unknown tenant status=%d body=%s", resp.Code, resp.Body.String())
	}
}
