package financialtest

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Seed an old physical attempt with real SQLC writes, then exercise the real
// account authority and owner fences using PostgreSQL's unchanged clock.
func TestFinancialComputeAccountPostgresMonthRecovery(t *testing.T) {
	for _, resume := range []string{"manual", "next_period"} {
		for _, editBefore := range []bool{false, true} {
			name := resume + "/retire_before_resume"
			if editBefore {
				name = resume + "/edit_before_initialization"
			}
			t.Run(name, func(t *testing.T) {
				store, pool, ctx := financialPostgres(t)
				f := newAccountComputeFixture(t, store, 153600, 1000, false)
				month := financial.ComputeMonth(time.Now())
				previous := month.AddDate(0, -1, 0)
				spec := f.policy.Spec
				spec.ResumeRule = resume
				id := uuid.NewString()
				q := sqlc.New()
				req := f.request(0)
				req, err := financial.BindComputeRequest(req)
				if err != nil {
					t.Fatal(err)
				}
				start := month.Add(-3 * time.Minute)
				funded := start.Add(api.FinancialComputeLeaseDuration + api.FinancialComputeTeardownReserve)
				gross, err := financial.ComputeCharge(req.BillableMB, start, funded, api.OverageMillicentsPerGBHour, api.SecondsPerGBHour)
				if err != nil {
					t.Fatal(err)
				}
				quantity, err := financial.ComputeQuantity(req.BillableMB, start, funded)
				if err != nil {
					t.Fatal(err)
				}
				lease := financial.ComputeLease{ID: req.LeaseID, Fence: req.Fence, RequestID: req.RequestID, RequestOperation: "acquire", Revision: 1, AccountID: f.account.ID, AppID: req.AppID, InstanceID: req.InstanceID, NodeID: req.NodeID, Incarnation: req.Incarnation, AttemptID: req.AttemptID, Fenced: true, Strict: true, AccountFunded: true, BillableMB: req.BillableMB, RateMillicents: api.OverageMillicentsPerGBHour, RateQuantity: api.SecondsPerGBHour, PeriodStart: previous, StartedAt: start, GrantedAt: start, RunUntil: start.Add(api.FinancialComputeLeaseDuration), FundedUntil: funded, Reserved: gross, QuantityReserved: quantity}
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(ctx) }()
				if _, err := q.FinancialBudgetInsert(ctx, tx, sqlc.FinancialBudgetInsertParams{ID: financialFixtureUUID(id), AccountID: financialFixtureUUID(f.account.ID), Spec: financialFixtureJSON(t, spec)}); err != nil {
					t.Fatal(err)
				}
				if err := q.FinancialBudgetRevisionInsert(ctx, tx, sqlc.FinancialBudgetRevisionInsertParams{AccountID: financialFixtureUUID(f.account.ID), PolicyID: financialFixtureUUID(id), Revision: 1, Actor: "fixture", Mutation: "created", Spec: financialFixtureJSON(t, spec)}); err != nil {
					t.Fatal(err)
				}
				appbook := financial.ComputeBook{AccountID: f.account.ID, AppID: req.AppID, PeriodStart: previous, PeriodEnd: month, Reserved: gross}
				if err := q.FinancialComputeBookPut(ctx, tx, sqlc.FinancialComputeBookPutParams{AccountID: financialFixtureUUID(f.account.ID), AppID: financialFixtureUUID(req.AppID), PeriodStart: financialFixtureTime(previous), PeriodEnd: financialFixtureTime(month), Payload: financialFixtureJSON(t, appbook)}); err != nil {
					t.Fatal(err)
				}
				accountbook := financial.ComputeBook{AccountID: f.account.ID, PeriodStart: previous, PeriodEnd: month, AllowanceGranted: quantity, QuantityReserved: quantity}
				if err := q.FinancialComputeAccountBookPut(ctx, tx, sqlc.FinancialComputeAccountBookPutParams{AccountID: financialFixtureUUID(f.account.ID), PeriodStart: financialFixtureTime(previous), PeriodEnd: financialFixtureTime(month), Payload: financialFixtureJSON(t, accountbook)}); err != nil {
					t.Fatal(err)
				}
				if n, err := q.FinancialComputeLeasePut(ctx, tx, sqlc.FinancialComputeLeasePutParams{ID: financialFixtureUUID(lease.ID), AccountID: financialFixtureUUID(f.account.ID), AppID: financialFixtureUUID(req.AppID), InstanceID: req.InstanceID, NodeID: financialFixtureUUID(req.NodeID), Incarnation: financialFixtureUUID(req.Incarnation), PeriodStart: financialFixtureTime(previous), Strict: true, StartedAt: financialFixtureTime(start), FundedUntil: financialFixtureTime(funded), Payload: financialFixtureJSON(t, lease)}); err != nil || n != 1 {
					t.Fatalf("lease: %d %v", n, err)
				}
				if err := q.FinancialComputeHoldPut(ctx, tx, sqlc.FinancialComputeHoldPutParams{AccountID: financialFixtureUUID(f.account.ID), PolicyID: financialFixtureUUID(id), Revision: 1, PeriodStart: financialFixtureTime(previous)}); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				policy, err := store.GetFinancialBudget(ctx, f.account.ID, id)
				if err != nil {
					t.Fatal(err)
				}
				// The old attempt retains its account allowance across the UTC reset.
				current, err := store.FinancialComputeBalance(ctx, policy, month)
				if err != nil || current.Initialized || current.UnconfirmedLeases != 1 || current.Held != (resume == "manual") {
					t.Fatalf("current period bypassed old funding: %+v %v", current, err)
				}
				if editBefore {
					policy, err = store.UpdateFinancialBudget(ctx, f.account.ID, policy.ID, policy.Revision, "operator", spec)
					if err != nil {
						t.Fatal(err)
					}
					current, err = store.FinancialComputeBalance(ctx, policy, month)
					if err != nil || current.Initialized || current.Held {
						t.Fatalf("policy edit minted a new allowance: %+v %v", current, err)
					}
				}
				owner := runtimeadmission.Identity{NodeID: lease.NodeID, Incarnation: uuid.NewString(), ProtocolVersion: 1}
				if err := store.RegisterComputeNodeRuntimeIdentity(ctx, owner); err != nil {
					t.Fatal(err)
				}
				if err := store.RegisterFinancialComputeOwner(ctx, owner.NodeID, owner.Incarnation); err != nil {
					t.Fatal(err)
				}
				stale := financial.ComputeRequest{LeaseID: lease.ID, Fence: lease.Fence, RequestID: uuid.NewString(), Revision: lease.Revision, AccountID: lease.AccountID, AppID: lease.AppID, InstanceID: lease.InstanceID, NodeID: lease.NodeID, Incarnation: lease.Incarnation}
				if _, err := store.RenewFinancialCompute(ctx, stale); !errors.Is(err, financial.ErrComputeFence) {
					t.Fatalf("previous owner renewed account funding: %v", err)
				}
				// A different app cannot consume a new grant until old physical
				// funding has settled, even after a policy edit.
				if _, err := store.AcquireFinancialCompute(ctx, f.request(1)); !errors.Is(err, financial.ErrComputeUnavailable) {
					t.Fatalf("old attempt escaped across apps: %v", err)
				}
				old, err := store.FinancialComputeBalance(ctx, policy, previous)
				if err != nil || old.AllowanceReserved != quantity {
					t.Fatalf("old funding: %+v %v", old, err)
				}
				f.settle(t, lease)
				old, err = store.FinancialComputeBalance(ctx, policy, previous)
				if err != nil || old.AllowanceReserved != 0 || old.AllowanceCommitted != quantity {
					t.Fatalf("old settlement: %+v %v", old, err)
				}
				fresh := f.request(1)
				grant, err := store.AcquireFinancialCompute(ctx, fresh)
				if resume == "manual" && !editBefore {
					if !errors.Is(err, financial.ErrComputeBudget) {
						t.Fatalf("manual hold reset: %v", err)
					}
					policy, err = store.UpdateFinancialBudget(ctx, f.account.ID, policy.ID, policy.Revision, "operator", spec)
					if err != nil {
						t.Fatal(err)
					}
					fresh = f.request(1)
					grant, err = store.AcquireFinancialCompute(ctx, fresh)
				}
				if err != nil || !grant.AccountFunded || !grant.PeriodStart.Equal(month) {
					t.Fatalf("current funding: %+v %v", grant, err)
				}
				f.settle(t, grant)
			})
		}
	}
}
