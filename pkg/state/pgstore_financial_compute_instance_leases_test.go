//go:build !no_pg

// adr: 20261102160622
package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// The reaper's bulk lease read answers, per instance, what
// FinancialComputeInstanceLease answers: the newest lease of that instance
// under its account and app, settled or not.
func TestPgFinancialComputeLatestInstanceLeases(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "compute-leases-bulk", "bulk")
	otherAccount, otherApp, _ := seedLiveDeploy(t, s, ctx, "compute-leases-other", "other")
	node, incarnation := uuid.NewString(), uuid.NewString()
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	insert := func(account, app, instance string, startedAt time.Time, settled bool) string {
		t.Helper()
		l := financial.ComputeLease{
			ID: uuid.NewString(), AccountID: account, AppID: app, InstanceID: instance, NodeID: node, Incarnation: incarnation,
			PeriodStart: financial.ComputeMonth(startedAt), StartedAt: startedAt, GrantedAt: startedAt,
			RunUntil: startedAt.Add(time.Minute), FundedUntil: startedAt.Add(2 * time.Minute),
		}
		if settled {
			at := startedAt.Add(time.Minute)
			l.SettledAt = &at
		}
		payload, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO financial_compute_leases(id,account_id,app_id,instance_id,node_id,incarnation,period_start,strict,started_at,funded_until,settled_at,payload)
 VALUES($1,$2,$3,$4,$5,$6,$7,false,$8,$9,$10,$11)`, l.ID, l.AccountID, l.AppID, l.InstanceID, l.NodeID, l.Incarnation, l.PeriodStart, l.StartedAt, l.FundedUntil, l.SettledAt, payload); err != nil {
			t.Fatal(err)
		}
		return l.ID
	}
	twice, once, none, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	insert(accountID, appID, twice, base, true)
	newest := insert(accountID, appID, twice, base.Add(10*time.Minute), false)
	onlyOne := insert(accountID, appID, once, base.Add(5*time.Minute), true)
	// Another app's instance is not read under this app's key.
	insert(otherAccount, otherApp, foreign, base, false)

	keys := []state.FinancialComputeInstanceKey{
		{AccountID: accountID, AppID: appID, InstanceID: twice},
		{AccountID: accountID, AppID: appID, InstanceID: once},
		{AccountID: accountID, AppID: appID, InstanceID: none},
		{AccountID: accountID, AppID: appID, InstanceID: foreign},
	}
	got, err := s.FinancialComputeLatestInstanceLeases(ctx, keys)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[twice].ID != newest || got[once].ID != onlyOne {
		t.Fatalf("FinancialComputeLatestInstanceLeases = %+v, want %s's newest lease %s and %s's %s", got, twice, newest, once, onlyOne)
	}
	for _, key := range keys {
		one, ok, err := s.FinancialComputeInstanceLease(ctx, key.AccountID, key.AppID, key.InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		bulk, inBulk := got[key.InstanceID]
		if ok != inBulk || one.ID != bulk.ID {
			t.Fatalf("instance %s: FinancialComputeInstanceLease = %s, %v; bulk = %s, %v", key.InstanceID, one.ID, ok, bulk.ID, inBulk)
		}
	}
}
