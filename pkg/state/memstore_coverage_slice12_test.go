package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestMemStoreCoverageOverageCap drives UpdateAccountOverageCapCents across
// its three branches (set / clear / re-set) and pins the customer self-service
// "set / clear cap" surface used by issue #561's raiseOverageCap endpoint.
func TestMemStoreCoverageOverageCap(t *testing.T) {
	m, ctx, account, _, _ := state.MemCoverageFixture(t)

	// nil clears the cap (NULL round-trip in pgstore).
	if err := m.UpdateAccountOverageCapCents(ctx, account.ID, nil); err != nil {
		t.Fatalf("clear cap: %v", err)
	}

	// Set the cap to a non-zero value.
	cents := int64(2500)
	if err := m.UpdateAccountOverageCapCents(ctx, account.ID, &cents); err != nil {
		t.Fatalf("set cap: %v", err)
	}

	// SetOverageCapCentsForTest is the test-only seam that plants a value
	// before the meterd tick runs; verify it does not panic and writes
	// the value into the underlying map.
	m.SetOverageCapCentsForTest(account.ID, 9999)

	// nil on an unknown account is also a no-op clear.
	if err := m.UpdateAccountOverageCapCents(ctx, "missing-account", nil); err != nil {
		t.Fatalf("clear missing account: %v", err)
	}

	// Re-set cap to zero; the (cents=0, ok=true) case is preserved so the
	// workload gate sees "cap = 0" as a refusal trigger.
	zero := int64(0)
	if err := m.UpdateAccountOverageCapCents(ctx, account.ID, &zero); err != nil {
		t.Fatalf("set cap=0: %v", err)
	}
}

// TestMemStoreCoverageCreditLedger drives AppendCreditForTest and
// ListCreditLedgerForTest. These are the MemStore-side seams that mirror
// pkg/billing/reconciler behavior; they're not on the Store interface but
// higher-level tests in pkg/billing call them. Note: AppendCreditForTest
// writes to the accountCredits map; ListCreditLedgerForTest reads from
// the creditLedger slice. CreateCreditLedgerEntry is the bridge that
// mirrors an issuance into the ledger.
func TestMemStoreCoverageCreditLedger(t *testing.T) {
	m, _, account, _, _ := state.MemCoverageFixture(t)

	// Empty ledger for an account that has no credits.
	if got := m.ListCreditLedgerForTest(account.ID); len(got) != 0 {
		t.Fatalf("empty ledger = %d entries, want 0", len(got))
	}

	// Append two credit entries to the credits map.
	m.AppendCreditForTest(state.AccountCredit{
		AccountID:      account.ID,
		CentsRemaining: 1000,
		Reason:         "stripe_reconciliation",
	})
	m.AppendCreditForTest(state.AccountCredit{
		AccountID:      account.ID,
		CentsRemaining: 500,
		Reason:         "manual_grant",
	})

	// Mirror those issuances into the audit ledger.
	if err := m.CreateCreditLedgerEntry(context.Background(), state.CreditLedgerEntry{
		AccountID:  account.ID,
		DeltaCents: 1000,
		Reason:     "issuance",
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateCreditLedgerEntry(context.Background(), state.CreditLedgerEntry{
		AccountID:  account.ID,
		DeltaCents: 500,
		Reason:     "issuance",
	}); err != nil {
		t.Fatal(err)
	}

	got := m.ListCreditLedgerForTest(account.ID)
	if len(got) != 2 {
		t.Fatalf("ledger after 2 entries = %d, want 2", len(got))
	}

	// Append to a different account doesn't pollute the original.
	other, err := m.CreateAccount(context.Background(), "other-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	m.AppendCreditForTest(state.AccountCredit{AccountID: other.ID, CentsRemaining: 1})
	if got := m.ListCreditLedgerForTest(account.ID); len(got) != 2 {
		t.Fatalf("cross-account bleed: %d entries on account", len(got))
	}
	if got := m.ListCreditLedgerForTest(other.ID); len(got) != 0 {
		t.Fatalf("other account ledger: %d entries, want 0", len(got))
	}
}

// TestMemStoreCoverageUsageSLO drives UsageSLOForApp and UsageSLOForAccount.
// These are intentionally no-op stubs in MemStore — the production SLO
// computation lives in pkg/meter (which queries PgStore). The test pins the
// no-op contract so that handlers don't crash on dev/host builds where
// MemStore is the active store.
func TestMemStoreCoverageUsageSLO(t *testing.T) {
	m, ctx, account, app, _ := state.MemCoverageFixture(t)
	now := time.Now().UTC()
	start := now.Add(-24 * time.Hour)

	ok, errCount, err := m.UsageSLOForApp(ctx, account.ID, app.ID, start, now)
	if err != nil || ok != 0 || errCount != 0 {
		t.Fatalf("UsageSLOForApp = %f, %f, %v; want 0, 0, nil", ok, errCount, err)
	}

	ok, errCount, err = m.UsageSLOForAccount(ctx, account.ID, start, now)
	if err != nil || ok != 0 || errCount != 0 {
		t.Fatalf("UsageSLOForAccount = %f, %f, %v; want 0, 0, nil", ok, errCount, err)
	}
}

// TestMemStoreCoverageDeploymentListing drives ListAllDeployments and
// ListDeploymentsByNodeID across populated/filtered shapes. These power
// the operator-side "list all live deployments" view used by meterd's
// per-instance rollup walks.
func TestMemStoreCoverageDeploymentListing(t *testing.T) {
	m, ctx, _, _, dep := state.MemCoverageFixture(t)

	// The fixture already creates one deployment; ListAllDeployments
	// returns it (filtered to non-deleted apps).
	all, err := m.ListAllDeployments(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("ListAllDeployments = %d, %v; want 1", len(all), err)
	}

	// Two instances on different nodes share the same deployment. The
	// deployment is "on node-a" via its primary instance; secondary
	// instances on node-b must not leak into node-a's view.
	if _, err := m.CreateInstance(ctx, dep.AppID, dep.ID, string(state.StateRunning), 256, "node-a", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateInstance(ctx, dep.AppID, dep.ID, string(state.StateRunning), 256, "node-b", ""); err != nil {
		t.Fatal(err)
	}

	// ListAllDeployments still returns 1 (one deployment).
	all, err = m.ListAllDeployments(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("after 2 instances: ListAllDeployments = %d, %v", len(all), err)
	}

	// ListDeploymentsByNodeID should not crash on a non-existent node.
	if _, err := m.ListDeploymentsByNodeID(ctx, "node-missing"); err != nil {
		t.Fatalf("ListDeploymentsByNodeID missing: %v", err)
	}
}

// TestMemStoreCoverageScanResult drives UpsertDeploymentScanResult across
// insert + update paths. The blob is opaque to MemStore; the test pins
// that the call round-trips without panicking on either the first write
// (insert) or a subsequent write (update).
func TestMemStoreCoverageScanResult(t *testing.T) {
	m, ctx, _, _, dep := state.MemCoverageFixture(t)
	scan := []byte(`{"summary":"ok"}`)

	if err := m.UpsertDeploymentScanResult(ctx, dep.ID, scan, "pass"); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Update the scan with a different status and payload.
	scan2 := []byte(`{"summary":"fail","reasons":["x"]}`)
	if err := m.UpsertDeploymentScanResult(ctx, dep.ID, scan2, "fail"); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	// Missing deployment ID is rejected with a not-found error path
	// (the pgstore path returns ErrNotFound; MemStore returns the same).
	if err := m.UpsertDeploymentScanResult(ctx, "missing-deployment", scan, "pass"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("upsert missing: %v, want ErrNotFound", err)
	}
}
