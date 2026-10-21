package state_test

// MemStore coverage for the issue #791 cron run-history surface: the
// durable Outcome stamped by the Complete/Fail write paths and the
// ListCronRunsForCron read.
//
// Kept as sub-tests under two top-level funcs so the suite's
// per-package timeout budget stays predictable.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedInvocationAppCtx is the contextcheck-clean twin of seedInvocationApp
// (pkg/state/memstore_invocations_test.go:16). The lint rule rejects
// any helper that calls context.Background() itself, so the test
// threads its own ctx through.
func seedInvocationAppCtx(t *testing.T, ctx context.Context) (*state.MemStore, string, string) {
	t.Helper()
	m := state.NewMemStore()
	acct, err := m.CreateAccount(ctx, "inv@localhost", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, state.App{
		AccountID: acct.ID,
		Slug:      "cron-runs-test",
		Type:      state.AppTypeApp,
		Status:    state.AppActive,
		RAMMB:     256,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return m, app.ID, acct.ID
}

// seedCronInvocation enqueues one cron-sourced row and claims it, so
// the caller can drive it to whichever terminal state it needs.
func seedCronInvocation(t *testing.T, ctx context.Context, m *state.MemStore, appID, acctID, cronID string, createdAt time.Time) state.Invocation {
	t.Helper()
	id := cronID
	inv, err := m.EnqueueInvocation(ctx, state.Invocation{
		AppID:     appID,
		AccountID: acctID,
		Source:    state.InvocationCron,
		Method:    "POST",
		Path:      "/cron",
		CronID:    &id,
		DueAt:     createdAt,
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	if _, err := m.ClaimInvocation(ctx, inv.ID, "inst-1", 30); err != nil {
		t.Fatalf("ClaimInvocation: %v", err)
	}
	return inv
}

// TestMemInvocationOutcome covers the write half: every terminal
// transition stamps a durable outcome, and a transient re-queue
// clears it (the row is non-terminal again, so it must not keep
// advertising a stale terminal classification).
func TestMemInvocationOutcome(t *testing.T) {
	ctx := context.Background()

	t.Run("complete stamps success", func(t *testing.T) {
		m, appID, acctID := seedInvocationAppCtx(t, ctx)
		inv := seedCronInvocation(t, ctx, m, appID, acctID, "cron-1", time.Now())
		if err := m.CompleteInvocation(ctx, inv.ID, nil); err != nil {
			t.Fatalf("CompleteInvocation: %v", err)
		}
		got := mustOutcome(t, ctx, m, inv.ID)
		if got != state.OutcomeSuccess {
			t.Errorf("outcome = %q, want success", got)
		}
	})

	t.Run("permanent fail defaults to failed", func(t *testing.T) {
		m, appID, acctID := seedInvocationAppCtx(t, ctx)
		inv := seedCronInvocation(t, ctx, m, appID, acctID, "cron-1", time.Now())
		if err := m.FailInvocation(ctx, inv.ID, "boom", 0, 0); err != nil {
			t.Fatalf("FailInvocation: %v", err)
		}
		if got := mustOutcome(t, ctx, m, inv.ID); got != state.OutcomeFailed {
			t.Errorf("outcome = %q, want failed", got)
		}
	})

	t.Run("WithOutcome records timeout", func(t *testing.T) {
		m, appID, acctID := seedInvocationAppCtx(t, ctx)
		inv := seedCronInvocation(t, ctx, m, appID, acctID, "cron-1", time.Now())
		if err := m.FailInvocation(ctx, inv.ID, "deadline", 0, 0, state.WithOutcome(state.OutcomeTimeout)); err != nil {
			t.Fatalf("FailInvocation: %v", err)
		}
		// The whole point of #791: a timeout is not just "failed".
		if got := mustOutcome(t, ctx, m, inv.ID); got != state.OutcomeTimeout {
			t.Errorf("outcome = %q, want timeout", got)
		}
	})

	t.Run("transient requeue leaves no outcome", func(t *testing.T) {
		m, appID, acctID := seedInvocationAppCtx(t, ctx)
		inv := seedCronInvocation(t, ctx, m, appID, acctID, "cron-1", time.Now())
		if err := m.FailInvocation(ctx, inv.ID, "blip", time.Minute, 0, state.WithOutcome(state.OutcomeTimeout)); err != nil {
			t.Fatalf("FailInvocation: %v", err)
		}
		got, err := m.InvocationByID(ctx, inv.ID)
		if err != nil {
			t.Fatalf("InvocationByID: %v", err)
		}
		if got.State != state.InvocationPending {
			t.Fatalf("state = %q, want pending", got.State)
		}
		if got.Outcome != nil {
			t.Errorf("outcome = %q on a re-queued row, want nil", *got.Outcome)
		}
	})

	t.Run("dead letter overrides caller outcome", func(t *testing.T) {
		m, appID, acctID := seedInvocationAppCtx(t, ctx)
		inv := seedCronInvocation(t, ctx, m, appID, acctID, "cron-1", time.Now())
		// budget=1 with attempts already at 1 (Claim bumped it) routes
		// to dead_letter; the caller's timeout hint must not win.
		if err := m.FailInvocation(ctx, inv.ID, "spent", time.Minute, 1, state.WithOutcome(state.OutcomeTimeout)); err != nil {
			t.Fatalf("FailInvocation: %v", err)
		}
		got, err := m.InvocationByID(ctx, inv.ID)
		if err != nil {
			t.Fatalf("InvocationByID: %v", err)
		}
		if got.State != state.InvocationDeadLetter {
			t.Fatalf("state = %q, want dead_letter", got.State)
		}
		if got.Outcome == nil || *got.Outcome != state.OutcomeDeadLetter {
			t.Errorf("outcome = %v, want dead_letter", got.Outcome)
		}
	})
}

// TestMemListCronRunsForCron covers the read half: the cron_id filter,
// newest-first ordering, the limit, and the cursor.
func TestMemListCronRunsForCron(t *testing.T) {
	ctx := context.Background()

	t.Run("filters by cron and orders newest first", func(t *testing.T) {
		m, appID, acctID := seedInvocationAppCtx(t, ctx)
		base := time.Now().Add(-3 * time.Hour)
		a1 := seedCronInvocation(t, ctx, m, appID, acctID, "cron-a", base)
		a2 := seedCronInvocation(t, ctx, m, appID, acctID, "cron-a", base.Add(time.Hour))
		seedCronInvocation(t, ctx, m, appID, acctID, "cron-b", base.Add(2*time.Hour))

		rows, err := m.ListCronRunsForCron(ctx, "cron-a", 10, "")
		if err != nil {
			t.Fatalf("ListCronRunsForCron: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("got %d rows, want 2 (cron-b leaked?)", len(rows))
		}
		if rows[0].ID != a2.ID || rows[1].ID != a1.ID {
			t.Errorf("order = [%s %s], want newest-first [%s %s]",
				rows[0].ID, rows[1].ID, a2.ID, a1.ID)
		}
	})

	t.Run("non-cron rows never appear", func(t *testing.T) {
		m, appID, acctID := seedInvocationAppCtx(t, ctx)
		if _, err := m.EnqueueInvocation(ctx, state.Invocation{
			AppID: appID, AccountID: acctID, Source: state.InvocationAsyncInvoke,
			Method: "POST", Path: "/x", DueAt: time.Now(), CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("EnqueueInvocation: %v", err)
		}
		rows, err := m.ListCronRunsForCron(ctx, "cron-a", 10, "")
		if err != nil {
			t.Fatalf("ListCronRunsForCron: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("got %d rows for a cron with no runs, want 0", len(rows))
		}
	})

	t.Run("limit and cursor page without overlap", func(t *testing.T) {
		m, appID, acctID := seedInvocationAppCtx(t, ctx)
		base := time.Now().Add(-5 * time.Hour)
		for i := range 4 {
			seedCronInvocation(t, ctx, m, appID, acctID, "cron-a", base.Add(time.Duration(i)*time.Hour))
		}
		first, err := m.ListCronRunsForCron(ctx, "cron-a", 2, "")
		if err != nil {
			t.Fatalf("page 1: %v", err)
		}
		if len(first) != 2 {
			t.Fatalf("page 1 len = %d, want 2", len(first))
		}
		next, err := m.ListCronRunsForCron(ctx, "cron-a", 2, first[1].ID)
		if err != nil {
			t.Fatalf("page 2: %v", err)
		}
		if len(next) != 2 {
			t.Fatalf("page 2 len = %d, want 2", len(next))
		}
		for _, a := range first {
			for _, b := range next {
				if a.ID == b.ID {
					t.Fatalf("row %s on both pages", a.ID)
				}
			}
		}
	})
}

func mustOutcome(t *testing.T, ctx context.Context, m *state.MemStore, id string) state.InvocationOutcome {
	t.Helper()
	got, err := m.InvocationByID(ctx, id)
	if err != nil {
		t.Fatalf("InvocationByID: %v", err)
	}
	if got.Outcome == nil {
		t.Fatalf("outcome = nil on a terminal row (state=%q)", got.State)
	}
	return *got.Outcome
}
