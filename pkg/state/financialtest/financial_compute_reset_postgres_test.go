package financialtest

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Persist a previous UTC month's crash state in a private database. PostgreSQL
// keeps its real clock: neither the host clock nor production authority accepts
// test-selected time. These are ledger fixtures, not physical retirement proof.
func TestFinancialComputePostgresMonthRecovery(t *testing.T) {
	for _, resume := range []string{"manual", "next_period"} {
		for _, editBefore := range []bool{false, true} {
			name := resume + "/resume_after_retirement"
			if editBefore {
				name = resume + "/edit_before_initialization"
			}
			t.Run(name, func(t *testing.T) {
				store, pool, ctx := financialPostgres(t)
				a := financialAccount(t, store)
				app, err := store.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "month-recovery", RAMMB: 128, MaxConcurrency: 2})
				if err != nil {
					t.Fatal(err)
				}
				node := financialLocalNode(t, ctx, store)
				owner := runtimeadmission.Identity{NodeID: node, Incarnation: uuid.NewString(), ProtocolVersion: 1}
				if err := store.RegisterComputeNodeRuntimeIdentity(ctx, owner); err != nil {
					t.Fatal(err)
				}
				if err := store.RegisterFinancialComputeOwner(ctx, node, owner.Incarnation); err != nil {
					t.Fatal(err)
				}
				currentMonth := financial.ComputeMonth(time.Now().UTC())
				previousMonth := currentMonth.AddDate(0, -1, 0)
				request := financial.ComputeRequest{AttemptID: uuid.NewString(), Fenced: true, AccountID: a.ID, AppID: app.ID, InstanceID: uuid.NewString(), NodeID: node, Incarnation: owner.Incarnation, BillableMB: api.BillableRAMMB(128)}
				request, err = financial.BindComputeRequest(request)
				if err != nil {
					t.Fatal(err)
				}
				started := currentMonth.Add(-5 * time.Minute)
				funded := started.Add(api.FinancialComputeLeaseDuration + api.FinancialComputeTeardownReserve)
				reserved, err := financial.ComputeCharge(request.BillableMB, started, funded, api.OverageMillicentsPerGBHour, api.SecondsPerGBHour)
				if err != nil {
					t.Fatal(err)
				}
				old := financial.ComputeLease{ID: request.LeaseID, Fence: request.Fence, RequestID: request.RequestID, RequestOperation: "acquire", Revision: 1,
					AttemptID: request.AttemptID, Fenced: true, AccountID: a.ID, AppID: app.ID, InstanceID: request.InstanceID, NodeID: node, Incarnation: owner.Incarnation,
					Strict: true, BillableMB: request.BillableMB, RateMillicents: api.OverageMillicentsPerGBHour, RateQuantity: api.SecondsPerGBHour,
					PeriodStart: previousMonth, StartedAt: started, GrantedAt: started, RunUntil: started.Add(api.FinancialComputeLeaseDuration), FundedUntil: funded, Reserved: reserved}
				spec := financialBudgetSpec()
				spec.Scope, spec.Mode, spec.Action, spec.Basis = financial.BudgetScope{Kind: "app", ID: app.ID}, "strict", "suspend_workloads", "gross_usage"
				spec.NotifyMillicents, spec.DrainSeconds, spec.ResumeRule = []int64{}, 0, resume
				policyID := uuid.NewString()
				q := sqlc.New()
				// Seed one internally consistent crash snapshot atomically using
				// generated queries. Subsequent reads/mutations use the real store.
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(ctx) }()
				if _, err := q.FinancialBudgetInsert(ctx, tx, sqlc.FinancialBudgetInsertParams{ID: financialFixtureUUID(policyID), AccountID: financialFixtureUUID(a.ID), Spec: financialFixtureJSON(t, spec)}); err != nil {
					t.Fatal(err)
				}
				if err := q.FinancialBudgetRevisionInsert(ctx, tx, sqlc.FinancialBudgetRevisionInsertParams{AccountID: financialFixtureUUID(a.ID), PolicyID: financialFixtureUUID(policyID), Revision: 1, Actor: "history-fixture", Mutation: "created", Spec: financialFixtureJSON(t, spec)}); err != nil {
					t.Fatal(err)
				}
				book := financial.ComputeBook{AccountID: a.ID, AppID: app.ID, PeriodStart: previousMonth, PeriodEnd: currentMonth, Committed: 7, Reserved: reserved}
				if err := q.FinancialComputeBookPut(ctx, tx, sqlc.FinancialComputeBookPutParams{AccountID: financialFixtureUUID(a.ID), AppID: financialFixtureUUID(app.ID), PeriodStart: financialFixtureTime(previousMonth), PeriodEnd: financialFixtureTime(currentMonth), Payload: financialFixtureJSON(t, book)}); err != nil {
					t.Fatal(err)
				}
				if count, err := q.FinancialComputeLeasePut(ctx, tx, sqlc.FinancialComputeLeasePutParams{ID: financialFixtureUUID(old.ID), AccountID: financialFixtureUUID(a.ID), AppID: financialFixtureUUID(app.ID), InstanceID: old.InstanceID, NodeID: financialFixtureUUID(node), Incarnation: financialFixtureUUID(old.Incarnation), PeriodStart: financialFixtureTime(previousMonth), Strict: true, StartedAt: financialFixtureTime(started), FundedUntil: financialFixtureTime(funded), Payload: financialFixtureJSON(t, old)}); err != nil || count != 1 {
					t.Fatalf("seed previous physical attempt: %d %v", count, err)
				}
				if err := q.FinancialComputeHoldPut(ctx, tx, sqlc.FinancialComputeHoldPutParams{AccountID: financialFixtureUUID(a.ID), PolicyID: financialFixtureUUID(policyID), Revision: 1, PeriodStart: financialFixtureTime(previousMonth)}); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				policy, err := store.GetFinancialBudget(ctx, a.ID, policyID)
				if err != nil {
					t.Fatal(err)
				}
				balance := func() financial.ComputeBalance {
					t.Helper()
					b, err := store.FinancialComputeBalance(ctx, policy, currentMonth)
					if err != nil {
						t.Fatal(err)
					}
					return b
				}
				if b := balance(); b.Initialized || b.UnconfirmedLeases != 1 || b.Held != (resume == "manual") {
					t.Fatalf("reset lost retained funding/hold: %+v", b)
				}
				owner.Incarnation = uuid.NewString()
				if err := store.RegisterComputeNodeRuntimeIdentity(ctx, owner); err != nil {
					t.Fatal(err)
				}
				if err := store.RegisterFinancialComputeOwner(ctx, node, owner.Incarnation); err != nil {
					t.Fatal(err)
				}
				fresh := request
				fresh.LeaseID, fresh.Fence, fresh.RequestID = "", "", ""
				fresh.AttemptID, fresh.InstanceID, fresh.Incarnation = uuid.NewString(), uuid.NewString(), owner.Incarnation
				if editBefore {
					policy, err = store.UpdateFinancialBudget(ctx, a.ID, policy.ID, policy.Revision, "operator", spec)
					if err != nil {
						t.Fatal("policy edit before initialization:", err)
					}
					if b := balance(); b.Initialized || b.Held || b.UnconfirmedLeases != 1 {
						t.Fatalf("editing intent bypassed retirement: %+v", b)
					}
				}
				for at := app.CreatedAt.Truncate(time.Minute); at.Before(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
					if err := store.RecordFinancialSamplingWindow(ctx, at, true, false); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := store.AcquireFinancialCompute(ctx, fresh); !errors.Is(err, financial.ErrComputeUnavailable) {
					t.Fatalf("new month reused unconfirmed old funding: %v", err)
				}
				if b, err := store.FinancialComputeBalance(ctx, policy, previousMonth); err != nil || b.ReservedMillicents != reserved || b.CommittedMillicents != 7 {
					t.Fatalf("reset/edit released old money: %+v %v", b, err)
				}
				mutation := financial.ComputeRequest{LeaseID: old.ID, Fence: old.Fence, RequestID: uuid.NewString(), Revision: old.Revision, AccountID: a.ID, AppID: app.ID, InstanceID: old.InstanceID, NodeID: node, Incarnation: old.Incarnation, BillableMB: old.BillableMB}
				if _, err := store.RenewFinancialCompute(ctx, mutation); !errors.Is(err, financial.ErrComputeFence) {
					t.Fatalf("old incarnation renewed after reset: %v", err)
				}
				settled, err := store.SettleFinancialCompute(ctx, mutation)
				if err != nil || settled.Reserved != 0 || settled.Committed != reserved {
					t.Fatalf("exact old-fence retirement: %+v %v", settled, err)
				}
				if replay, err := store.SettleFinancialCompute(ctx, mutation); err != nil || replay.Revision != settled.Revision {
					t.Fatalf("old-period settlement replay: %+v %v", replay, err)
				}
				grant, err := store.AcquireFinancialCompute(ctx, fresh)
				if resume == "manual" && !editBefore {
					if !errors.Is(err, financial.ErrComputeBudget) || !balance().Held {
						t.Fatalf("manual hold did not survive UTC reset: %v", err)
					}
					policy, err = store.UpdateFinancialBudget(ctx, a.ID, policy.ID, policy.Revision, "operator", spec)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := store.AcquireFinancialCompute(ctx, fresh); !errors.Is(err, financial.ErrComputeBudget) {
						t.Fatalf("operator edit revived a denied wake: %v", err)
					}
					fresh.AttemptID, fresh.InstanceID = uuid.NewString(), uuid.NewString()
					grant, err = store.AcquireFinancialCompute(ctx, fresh)
				}
				if err != nil || !grant.PeriodStart.Equal(currentMonth) || !grant.Strict || grant.Reserved <= 0 {
					t.Fatalf("new-period grant after retirement: %+v %v", grant, err)
				}
				if b := balance(); !b.Initialized || b.Held || b.UnconfirmedLeases != 1 || b.ReservedMillicents != grant.Reserved || b.CommittedMillicents != 0 {
					t.Fatalf("prior month contaminated new funding: %+v", b)
				}
				if _, err := store.AcquireFinancialCompute(ctx, request); !errors.Is(err, financial.ErrComputeFence) {
					t.Fatalf("settled prior-month wake replay acquired authority: %v", err)
				}
				if seconds, err := store.FinancialComputeBillingSeconds(ctx, a.ID, app.ID, old.InstanceID, currentMonth, 60); err != nil || seconds != 0 {
					t.Fatalf("stale old-month instance billed into reset: %d %v", seconds, err)
				}
			})
		}
	}
}

func financialFixtureUUID(value string) pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.MustParse(value), Valid: true}
}

func financialFixtureTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func financialFixtureJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
