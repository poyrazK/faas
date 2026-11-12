// adr: 20261110160000 — slice 3: an egress overage election the provider
// bills live lifts the databases' allowance too; amendment 2026-11-11: each
// database's allowance is Neon's 500 GB per project, apart from app egress.
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
	if api.ManagedPostgresDatabaseEgressAllowanceBytes != 500_000_000_000 {
		t.Fatalf("database egress allowance = %d, want Neon's 500 GB per project", api.ManagedPostgresDatabaseEgressAllowanceBytes)
	}
	read := func() bool {
		t.Helper()
		allowance, err := srv.ManagedPostgresEgressAllowance(t.Context(), acct.ID, now)
		if err != nil || allowance.CeilingBytes != api.ManagedPostgresDatabaseEgressAllowanceBytes {
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

func TestManagedPostgresEgressAllowanceNeedsDatabasesOnThePlan(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	srv, acct := *e.S(), *e.Acct()
	if api.ManagedPostgresIncluded(api.PlanFree) {
		t.Skip("Free includes managed PostgreSQL")
	}
	allowance, err := srv.ManagedPostgresEgressAllowance(t.Context(), acct.ID, time.Now().UTC())
	if err != nil || allowance.CeilingBytes != 0 {
		t.Fatalf("allowance on a plan without databases = %+v, %v; want none", allowance, err)
	}
}
