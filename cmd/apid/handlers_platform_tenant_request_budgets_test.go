package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 240
func TestPlatformTenantRequestBudgetAPI(t *testing.T) {
	e := setup(t, api.PlanPro)
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID,
		"budget-api-customer", "Budget API customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/account/platform-tenants/" + tenant.ID + "/request-budget"
	read := func() api.PlatformTenantRequestBudgetResponse {
		t.Helper()
		resp := e.do(t, http.MethodGet, path, nil, nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("get budget: %d %s", resp.Code, resp.Body.String())
		}
		var budget api.PlatformTenantRequestBudgetResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &budget); err != nil {
			t.Fatal(err)
		}
		return budget
	}
	if got := read(); got.Configured || got.TenantID != tenant.ID {
		t.Fatalf("default budget=%+v", got)
	}
	minute, day := int64(8), int64(100)
	input := api.SetPlatformTenantRequestBudgetRequest{MaxRequestsPerMinute: &minute, MaxRequestsPerDay: &day}
	put := e.do(t, http.MethodPut, path, input, nil)
	if put.Code != http.StatusOK {
		t.Fatalf("put budget: %d %s", put.Code, put.Body.String())
	}
	first := read()
	if !first.Configured || first.MaxRequestsPerMinute != 8 || first.MaxRequestsPerDay != 100 || first.UpdatedAt == nil {
		t.Fatalf("configured budget=%+v", first)
	}
	if replay := e.do(t, http.MethodPut, path, input, nil); replay.Code != http.StatusOK {
		t.Fatalf("idempotent put: %d %s", replay.Code, replay.Body.String())
	}
	if got := read(); !got.UpdatedAt.Equal(*first.UpdatedAt) {
		t.Fatalf("no-op policy replay changed updated_at: %s -> %s", first.UpdatedAt, got.UpdatedAt)
	}
	for _, invalid := range []api.SetPlatformTenantRequestBudgetRequest{
		{MaxRequestsPerMinute: &minute},
		{MaxRequestsPerMinute: ptrInt64(-1), MaxRequestsPerDay: &day},
		{MaxRequestsPerMinute: ptrInt64(api.MaxPlatformTenantRequestsPerMinute + 1), MaxRequestsPerDay: &day},
	} {
		if resp := e.do(t, http.MethodPut, path, invalid, nil); resp.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid budget status=%d body=%s", resp.Code, resp.Body.String())
		}
	}
	if resp := e.do(t, http.MethodGet, "/v1/account/platform-tenants/"+uuid.NewString()+"/request-budget", nil, nil); resp.Code != http.StatusNotFound {
		t.Fatalf("unknown tenant status=%d body=%s", resp.Code, resp.Body.String())
	}
}
