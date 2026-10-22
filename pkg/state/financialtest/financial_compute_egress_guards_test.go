package financialtest

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFinancialEgressActivationGuards(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store computeTestStore = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = financialPostgres(t)
			}
			f := newAccountComputeFixture(t, store, 1000000, 100)
			l, err := store.AcquireFinancialCompute(t.Context(), f.request(0))
			if err != nil {
				t.Fatal(err)
			}
			spec := f.policy.Spec
			spec.Meters = []string{"compute", "egress"}
			if _, err := store.UpdateFinancialBudget(t.Context(), f.account.ID, f.policy.ID, f.policy.Revision, "test", spec); !errors.Is(err, financial.ErrComputeUnavailable) {
				t.Fatalf("live compute-only attempt admitted egress policy: %v", err)
			}
			f.settle(t, l)
			if _, err := store.UpdateFinancialBudget(t.Context(), f.account.ID, f.policy.ID, f.policy.Revision, "test", spec); !errors.Is(err, financial.ErrComputeUnavailable) {
				t.Fatalf("missing historical egress pricing admitted: %v", err)
			}
		})
	}
}

func TestFinancialEgressPostgresLegacyWriters(t *testing.T) {
	store, pool, ctx := financialPostgres(t)
	f := newEgressFixture(t, store, 1000000, 0, 100)
	l, err := store.AcquireFinancialCompute(ctx, f.request(0))
	if err != nil {
		t.Fatal(err)
	}
	// The old financial writer records execution but knows no byte gate. Its
	// raw write must not bypass authority just because validation was old.
	l.ID = uuid.NewString()
	l.InstanceID = uuid.NewString()
	l.EgressFunded = false
	payload, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO financial_compute_leases(id,account_id,app_id,instance_id,node_id,incarnation,period_start,strict,started_at,funded_until,payload)
 VALUES($1,$2,$3,$4,$5,$6,$7,true,$8,$9,$10)`, l.ID, l.AccountID, l.AppID, l.InstanceID, l.NodeID, l.Incarnation, l.PeriodStart, l.StartedAt, l.FundedUntil, payload)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "financial_egress_owner_funded" {
		t.Fatalf("legacy lease bypass: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO financial_price_snapshots(account_id,period_start,period_end,meter,version,price,plan,effective_from,delivery_mode)
 SELECT account_id,period_start,period_end,meter,'legacy-egress-upgrade',jsonb_set(price,'{millicents_per_unit}','2'),plan,effective_from,delivery_mode
 FROM financial_price_snapshots WHERE account_id=$1 AND meter='egress' LIMIT 1`, f.account.ID)
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "financial_egress_price_ceiling" {
		t.Fatalf("legacy pricing bypass: %v", err)
	}
	// Even after a policy is disabled, an older book/receipt serializer cannot
	// erase the new fields and make past byte funding spendable again.
	_, err = pool.Exec(ctx, `UPDATE financial_compute_account_books SET payload=payload-'egress' WHERE account_id=$1`, f.account.ID)
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "financial_egress_book_monotonic" {
		t.Fatalf("legacy book discarded byte authority: %v", err)
	}
	_, err = pool.Exec(ctx, `UPDATE financial_compute_leases SET payload=payload-'egress_funded'-'egress_granted_bytes'-'egress_sequence' WHERE account_id=$1 AND settled_at IS NULL`, f.account.ID)
	if !errors.As(err, &pgerr) || (pgerr.ConstraintName != "financial_egress_lease_monotonic" && pgerr.ConstraintName != "financial_egress_owner_funded") {
		t.Fatalf("legacy receipt discarded byte authority: %v", err)
	}

	// A fresh old node cannot attest byte enforcement merely by registering
	// its old compute protocol. An upgraded registration can do so explicitly.
	node := f.owners[0]
	_, err = pool.Exec(ctx, `UPDATE financial_compute_owners SET egress_budget_incarnation=NULL WHERE node_id=$1`, node.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	r := f.request(0)
	r.NodeID = node.NodeID
	r.Incarnation = node.Incarnation
	if _, err := store.AcquireFinancialCompute(ctx, r); !errors.Is(err, financial.ErrComputeUnavailable) {
		t.Fatalf("old owner funded egress: %v", err)
	}
	if err := store.RegisterFinancialComputeOwner(ctx, node.NodeID, node.Incarnation); err != nil {
		t.Fatal(err)
	}
	_, err = store.AcquireFinancialCompute(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
}

func TestFinancialEgressUTCResetRetainsOldBytes(t *testing.T) {
	store := state.NewMemStore()
	f := newEgressFixture(t, store, 1000000, api.FinancialEgressCreditBytes, 100)
	l, err := store.AcquireFinancialCompute(t.Context(), f.request(0))
	if err != nil {
		t.Fatal(err)
	}
	old := f.balance(t)
	next := financial.ComputeMonth(time.Now()).AddDate(0, 1, 0)
	store.SetClockForTest(func() time.Time { return next })
	b, err := store.FinancialComputeBalance(t.Context(), f.policy, next)
	if err != nil || b.Initialized || b.Egress == nil || b.Egress.Initialized || b.UnconfirmedLeases != 1 {
		t.Fatalf("reset visibility: %+v %v", b, err)
	}
	if _, err := store.AcquireFinancialCompute(t.Context(), f.request(1)); !errors.Is(err, financial.ErrComputeUnavailable) {
		t.Fatalf("reset reclaimed physical bytes: %v", err)
	}
	before, err := store.FinancialComputeBalance(t.Context(), f.policy, next.Add(-time.Minute))
	if err != nil || before.Egress.ReservedBytes != old.Egress.ReservedBytes {
		t.Fatalf("old month released: %+v %v", before, err)
	}
	settled, err := store.SettleFinancialCompute(t.Context(), jobComputeMutation(l))
	if err != nil {
		t.Fatal(err)
	}
	if settled.SettledAt == nil {
		t.Fatal("old-incarnation retirement not settled")
	}
}

func TestFinancialEgressPostgresRetainsAuthorityAfterPolicyDeletion(t *testing.T) {
	store, pool, ctx := financialPostgres(t)
	f := newEgressFixture(t, store, 1000000, 0, 100)
	l, err := store.AcquireFinancialCompute(ctx, f.request(0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteFinancialBudget(ctx, f.account.ID, f.policy.ID, f.policy.Revision, "test"); err != nil {
		t.Fatal(err)
	}
	var pgerr *pgconn.PgError
	_, err = pool.Exec(ctx, `UPDATE financial_compute_account_books SET payload=payload-'egress' WHERE account_id=$1`, f.account.ID)
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "financial_egress_book_monotonic" {
		t.Fatalf("deleted policy erased byte book: %v", err)
	}
	_, err = pool.Exec(ctx, `UPDATE financial_compute_leases SET payload=payload-'egress_funded'-'egress_granted_bytes'-'egress_sequence' WHERE id=$1`, l.ID)
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "financial_egress_lease_monotonic" {
		t.Fatalf("deleted policy erased byte receipt: %v", err)
	}
	if _, err := store.SettleFinancialCompute(ctx, jobComputeMutation(l)); err != nil {
		t.Fatal(err)
	}
	if b := f.balance(t); b.Egress.ReservedBytes != 0 || b.Egress.CommittedBytes != l.EgressGranted {
		t.Fatalf("guard prevented correct retirement: %+v", b)
	}
}
