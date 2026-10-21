package main_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1319, 1322, 1324, 1325 — rate-card-create sends an app unit and
// fees, trial-set sends days, and the account-level overdue verbs need no
// tenant.
func TestCmdPlatformTenantControls(t *testing.T) {
	t.Setenv("FAAS_API_MONETIZATION_ENABLED", "1") // adr: 20261018090210 — exercises released monetization
	var card api.CreatePlatformTenantRateCardRequest
	var trial api.PutAPIConsumerTrialRequest
	var policy api.PutAPIConsumerBillingPolicyRequest
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	withConsumersTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/account/platform-tenants/t1/rate-cards":
			_ = json.NewDecoder(r.Body).Decode(&card)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.PlatformTenantRateCardResponse{ID: "c1", TenantID: "t1", Currency: "EUR", Unit: card.Unit,
				EffectiveFrom: at, CreatedAt: at})
		case "PUT /v1/account/platform-tenants/t1/trial":
			_ = json.NewDecoder(r.Body).Decode(&trial)
			_ = json.NewEncoder(w).Encode(api.PlatformTenantTrialResponse{TenantID: "t1", StartsAt: at, EndsAt: at.AddDate(0, 0, 30), Active: true})
		case "PUT /v1/account/platform-tenant-billing-policy":
			_ = json.NewDecoder(r.Body).Decode(&policy)
			_ = json.NewEncoder(w).Encode(api.PlatformTenantBillingPolicyResponse{BlockOverdueAfterDays: 14, UpdatedAt: at})
		case "GET /v1/account/platform-tenant-overdue-holds":
			_ = json.NewEncoder(w).Encode(api.PlatformTenantOverdueHoldListResponse{Holds: []api.PlatformTenantOverdueHoldResponse{}})
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	if code := main.CmdPlatformTenants([]string{"rate-card-create", "--id", "t1", "--currency", "EUR", "--price-millicents", "2",
		"--unit", "app:tokens", "--base-fee-millicents", "4900000", "--minimum-millicents", "100", "--prorate-monthly-fees"}); code != 0 {
		t.Fatalf("rate-card-create exit %d", code)
	}
	if card.Unit != "app:tokens" || card.BaseFeeMillicentsPerMonth != 4900000 || card.MinimumMillicentsPerMonth != 100 || !card.ProrateMonthlyFees {
		t.Fatalf("card = %+v", card)
	}
	if code := main.CmdPlatformTenants([]string{"trial-set", "--id", "t1", "--days", "30"}); code != 0 || trial.Days != 30 {
		t.Fatalf("trial-set exit %d, request %+v", code, trial)
	}
	if code := main.CmdPlatformTenants([]string{"trial-set", "--id", "t1"}); code == 0 {
		t.Fatal("trial-set without --days or --ends-at succeeded")
	}
	if code := main.CmdPlatformTenants([]string{"billing-policy-set", "--block-overdue-after-days", "14"}); code != 0 ||
		policy.BlockOverdueAfterDays == nil || *policy.BlockOverdueAfterDays != 14 {
		t.Fatalf("billing-policy-set exit %d, request %+v", code, policy)
	}
	if code := main.CmdPlatformTenants([]string{"overdue-holds"}); code != 0 {
		t.Fatalf("overdue-holds exit %d", code)
	}
}
