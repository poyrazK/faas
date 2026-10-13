package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing"
)

func TestBillingStatusCustomerRoute(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	(*e.S()).WithBillingProviderName("stripe")
	if err := (*e.Store()).UpdateAccountProviderCustomerID(context.Background(), (*e.Acct()).ID, "cus_customer"); err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).UpdateAccountStripeSubscriptionItem(context.Background(), (*e.Acct()).ID, "si_customer"); err != nil {
		t.Fatal(err)
	}

	rec := e.Do(t, http.MethodGet, "/v1/billing/status", nil, nil)
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
	e := apidserver.Setup(t, api.PlanFree)
	req, err := http.NewRequest(http.MethodGet, "/v1/billing/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, req.Method, req.URL.Path, nil, map[string]string{"Authorization": ""})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestBillingStatusDisabledIsTruthful(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	(*e.S()).WithBillingProviderName("stripe").WithBillingMode(billing.ModeDisabled)
	rec := e.Do(t, http.MethodGet, "/v1/billing/status", nil, nil)
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
	if url := (*e.S()).BillingPortalURLForProvider(context.Background(), (*e.Acct())); url != "" {
		t.Fatalf("disabled mode exposed portal URL %q", url)
	}
}
