// adr: 20261110160000 — slice 3: an egress overage election the provider
// bills live lifts the databases' share of the allowance too.
package apidserver_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing"
)

func TestManagedPostgresEgressAllowanceFollowsTheElection(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv, store, acct := *e.S(), *e.Store(), *e.Acct()
	now := time.Now().UTC()
	limits, _ := api.LimitsFor(api.PlanPro)
	read := func() bool {
		t.Helper()
		allowance, err := srv.ManagedPostgresEgressAllowance(t.Context(), acct.ID, now)
		if err != nil || allowance.CeilingBytes != limits.MonthlyEgressGiB<<30 {
			t.Fatalf("allowance = %+v, %v", allowance, err)
		}
		return allowance.OverageLifted
	}
	srv.WithBillingProvider(apidserver.NewUsageSummaryEgressProvider(billing.MeterDeliveryLive))
	if read() {
		t.Fatal("lifted without an election")
	}
	if _, err := store.SetEgressOverage(t.Context(), acct.ID, true, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !read() {
		t.Fatal("an election billed live did not lift the databases' allowance")
	}
	srv.WithBillingProvider(apidserver.NewUsageSummaryEgressProvider(billing.MeterDeliveryShadow))
	if read() {
		t.Fatal("an election the provider only shadows lifted the allowance")
	}
}
