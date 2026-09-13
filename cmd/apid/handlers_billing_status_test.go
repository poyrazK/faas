package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
)

func TestBillingStatusCustomerRoute(t *testing.T) {
	e := setup(t, api.PlanScale)
	e.s.WithBillingProviderName("stripe")
	if err := e.store.UpdateAccountProviderCustomerID(context.Background(), e.acct.ID, "cus_customer"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateAccountStripeSubscriptionItem(context.Background(), e.acct.ID, "si_customer"); err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, http.MethodGet, "/v1/billing/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got api.BillingStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Mode != "live" || !got.Enabled || got.Provider != "stripe" {
		t.Fatalf("deployment projection = %+v", got)
	}
	if got.Plan != api.PlanScale || got.AccountStatus != "active" {
		t.Fatalf("account projection = %+v", got)
	}
	if !got.CustomerConfigured || !got.SubscriptionConfigured {
		t.Fatalf("provider attachment projection = %+v", got)
	}
}

func TestBillingStatusRequiresCustomerAuthentication(t *testing.T) {
	e := setup(t, api.PlanFree)
	req, err := http.NewRequest(http.MethodGet, "/v1/billing/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, req.Method, req.URL.Path, nil, map[string]string{"Authorization": ""})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestBillingStatusDisabledIsTruthful(t *testing.T) {
	e := setup(t, api.PlanFree)
	e.s.WithBillingProviderName("stripe").WithBillingMode(billing.ModeDisabled)
	rec := e.do(t, http.MethodGet, "/v1/billing/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got api.BillingStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Mode != "disabled" || got.Enabled || got.UsageReconciliationEnabled {
		t.Fatalf("disabled projection = %+v", got)
	}
	if url := e.s.billingPortalURLForProvider(context.Background(), e.acct); url != "" {
		t.Fatalf("disabled mode exposed portal URL %q", url)
	}
}
