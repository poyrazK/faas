package financialtest

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestFinancialComputeAccountOwnerCompatibility(t *testing.T) {
	store, pool, ctx := financialPostgres(t)
	f := newAccountComputeFixture(t, store, 153600, 1000)
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: uuid.NewString(), TargetURL: "unix:///tmp/account-legacy.sock", VPCPUs: 2, MemMB: 4096, MaxConcurrency: 2, AdmissionCeilingMB: 3000, VCPUBudget: 2, Active: true, Lifecycle: state.NodeLifecycleActive})
	if err != nil {
		t.Fatal(err)
	}
	owner := runtimeadmission.Identity{NodeID: node.ID, Incarnation: uuid.NewString(), ProtocolVersion: 1}
	if err := store.RegisterComputeNodeRuntimeIdentity(ctx, owner); err != nil {
		t.Fatal(err)
	}
	q := sqlc.New()
	legacyRegister := func() error {
		// A NULL proof has the exact legacy upsert semantics: new rows have
		// no account capability, and updates preserve an existing proof.
		return q.FinancialComputeOwnerPut(ctx, pool, sqlc.FinancialComputeOwnerPutParams{NodeID: financialFixtureUUID(node.ID), Incarnation: financialFixtureUUID(owner.Incarnation)})
	}
	if err := legacyRegister(); err != nil {
		t.Fatal(err)
	}
	if valid, err := store.FinancialComputeOwnerValid(ctx, node.ID, owner.Incarnation); err != nil || valid {
		t.Fatalf("legacy owner accepted: %t %v", valid, err)
	}
	if ready, err := store.FinancialComputeReady(ctx, f.account.ID, ""); err != nil || ready {
		t.Fatalf("legacy fleet ready: %t %v", ready, err)
	}
	if _, err := store.CreateFinancialBudget(ctx, f.account.ID, uuid.NewString(), "test", f.policy.Spec); !errors.Is(err, financial.ErrComputeUnavailable) {
		t.Fatalf("activation accepted legacy owner: %v", err)
	}
	r := f.request(0)
	r.NodeID = node.ID
	r.Incarnation = owner.Incarnation
	if _, err := store.AcquireFinancialCompute(ctx, r); !errors.Is(err, financial.ErrComputeUnavailable) {
		t.Fatalf("legacy owner received funding: %v", err)
	}
	if err := store.RegisterFinancialComputeOwner(ctx, node.ID, owner.Incarnation); err != nil {
		t.Fatal(err)
	}
	if valid, err := store.FinancialComputeOwnerValid(ctx, node.ID, owner.Incarnation); err != nil || !valid {
		t.Fatalf("upgraded owner rejected: %t %v", valid, err)
	}
	l, err := store.AcquireFinancialCompute(ctx, r)
	if err != nil || !l.AccountFunded {
		t.Fatalf("upgraded funding: %+v %v", l, err)
	}
	f.settle(t, l)
	// Daemons mint a new native incarnation at every start. The old binary
	// cannot erase the previous account capability while replacing its owner.
	owner.Incarnation = uuid.NewString()
	if err := store.RegisterComputeNodeRuntimeIdentity(ctx, owner); err != nil {
		t.Fatal(err)
	}
	var check *pgconn.PgError
	if err := legacyRegister(); !errors.As(err, &check) || check.Code != "23514" || (check.ConstraintName != "financial_compute_owner_account_incarnation_check" && check.ConstraintName != "financial_compute_job_owner_incarnation") {
		t.Fatalf("legacy downgrade registration succeeded: %v", err)
	}
	r = f.request(1)
	r.NodeID = node.ID
	r.Incarnation = owner.Incarnation
	if _, err := store.AcquireFinancialCompute(ctx, r); !errors.Is(err, financial.ErrComputeUnavailable) {
		t.Fatalf("downgrade received funding: %v", err)
	}
	if err := store.RegisterFinancialComputeOwner(ctx, node.ID, owner.Incarnation); err != nil {
		t.Fatal(err)
	}
	// Failed registration and grants never changed the prior account book.
	if b := f.balance(t); b.UnconfirmedLeases != 0 || b.AllowanceReserved != 0 || b.Held {
		t.Fatalf("compatibility failure changed funding: %+v", b)
	}
	l, err = store.AcquireFinancialCompute(ctx, r)
	if err != nil || !l.AccountFunded {
		t.Fatalf("replacement funding: %+v %v", l, err)
	}
	f.settle(t, l)
	if _, err := store.FinancialComputeBalance(ctx, f.policy, time.Now()); err != nil {
		t.Fatal(err)
	}
}
