package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func reviewedCronRequest(c state.Cron, deletion bool) api.CronMutationRequest {
	r := api.CronMutationRequest{RequestID: uuid.NewString(), AppID: c.AppID, CronID: c.ID, ExpectedState: state.CronReviewToken(c), ReceiptProof: strings.Repeat("a", 64), ExpiresAt: time.Now().UTC().Add(time.Minute), Delete: deletion}
	if !deletion {
		path := "/updated"
		r.Path = &path
	}
	return r
}

func TestCronMutationUpdateGuardAndRecovery(t *testing.T) {
	cronAnchorBackends(t, func(t *testing.T, ctx context.Context, store state.Store, appID, accountID string) {
		s := store.(state.CronMutationStore)
		c, err := store.CreateCron(ctx, appID, "0 3 * * *", "/original", true)
		if err != nil {
			t.Fatal(err)
		}
		r := reviewedCronRequest(c, false)
		if err := store.MarkCronFired(ctx, c.ID, time.Now()); err != nil {
			t.Fatal(err)
		}
		first, err := s.ApplyCronMutation(ctx, accountID, appID, r)
		if err != nil || first.Status != "completed" {
			t.Fatalf("update: %+v %v", first, err)
		}
		// A lost reply reuses the exact attempt; subsequent cron edits are not
		// overwritten by recovery of its original historical receipt.
		later := "/later"
		if _, err := store.UpdateCron(ctx, c.ID, nil, &later, nil, nil); err != nil {
			t.Fatal(err)
		}
		replayed, err := s.ApplyCronMutation(ctx, accountID, appID, r)
		if err != nil || replayed != first {
			t.Fatalf("replay: %+v %v", replayed, err)
		}
		current, _ := store.CronByID(ctx, c.ID)
		if current.Path != later {
			t.Fatal("receipt replay repeated the write")
		}
		changed := r
		changed.Path = &later
		if _, err := s.ApplyCronMutation(ctx, accountID, appID, changed); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("changed retry: %v", err)
		}
		for _, proof := range []string{"", strings.Repeat("b", 64)} {
			if _, err := s.CronMutationReceipt(ctx, accountID, appID, r.RequestID, proof); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("proof accepted: %v", err)
			}
		}
		if _, err := s.CronMutationReceipt(ctx, uuid.NewString(), appID, r.RequestID, r.ReceiptProof); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("foreign receipt: %v", err)
		}
		if _, err := s.ApplyCronMutation(ctx, accountID, appID, reviewedCronRequest(c, false)); !errors.Is(err, state.ErrCronReviewConflict) {
			t.Fatalf("stale update: %v", err)
		}
	})
}

func TestCronMutationDeletionFencesEveryBatch(t *testing.T) {
	for _, stopAt := range []string{"cancel_pending", "detach_invocations", "detach_tasks", "delete"} {
		t.Run(stopAt, func(t *testing.T) {
			cronAnchorBackends(t, func(t *testing.T, ctx context.Context, store state.Store, appID, accountID string) {
				c, err := store.CreateCron(ctx, appID, "0 3 * * *", "/original", true)
				if err != nil {
					t.Fatal(err)
				}
				s, r := store.(state.CronMutationStore), reviewedCronRequest(c, true)
				var receipt api.CronMutationReceipt
				for i := 0; i < 10; i++ {
					receipt, err = s.ApplyCronMutation(ctx, accountID, appID, r)
					if err != nil {
						t.Fatal(err)
					}
					if receipt.Phase == stopAt {
						break
					}
				}
				if receipt.Phase != stopAt {
					t.Fatalf("phase=%s", receipt.Phase)
				}
				changed := "/someone-else"
				if _, err := store.UpdateCron(ctx, c.ID, nil, &changed, nil, nil); err != nil {
					t.Fatal(err)
				}
				after, err := s.ApplyCronMutation(ctx, accountID, appID, r)
				if err != nil || after.Status != "conflict" || after.Cancelled != receipt.Cancelled || after.DetachedInvocations != receipt.DetachedInvocations || after.DetachedTasks != receipt.DetachedTasks {
					t.Fatalf("stale batch: %+v %v", after, err)
				}
				if current, err := store.CronByID(ctx, c.ID); err != nil || current.Path != changed {
					t.Fatalf("changed cron was removed: %+v %v", current, err)
				}
			})
		})
	}
}

func TestCronMutationDeletionReceiptSurvivesRemoval(t *testing.T) {
	cronAnchorBackends(t, func(t *testing.T, ctx context.Context, store state.Store, appID, accountID string) {
		c, err := store.CreateCron(ctx, appID, "0 3 * * *", "/original", true)
		if err != nil {
			t.Fatal(err)
		}
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID, Source: state.InvocationCron, CronID: &c.ID, Method: "POST", Path: "/original", DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		s, r := store.(state.CronMutationStore), reviewedCronRequest(c, true)
		var receipt api.CronMutationReceipt
		for i := 0; i < 12; i++ {
			receipt, err = s.ApplyCronMutation(ctx, accountID, appID, r)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Status == "completed" {
				break
			}
		}
		if receipt.Status != "completed" || receipt.Cancelled != 1 || receipt.DetachedInvocations != 1 {
			t.Fatalf("delete: %+v", receipt)
		}
		if _, err := store.CronByID(ctx, c.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("cron remains: %v", err)
		}
		if got, err := store.InvocationByID(ctx, inv.ID); err != nil || got.State != state.InvocationCancelled {
			t.Fatalf("queued run survived: %+v %v", got, err)
		}
		read, err := s.CronMutationReceipt(ctx, accountID, appID, r.RequestID, r.ReceiptProof)
		if err != nil || read != receipt {
			t.Fatalf("receipt lost after delete: %+v %v", read, err)
		}
		replay, err := s.ApplyCronMutation(ctx, accountID, appID, r)
		if err != nil || replay != receipt {
			t.Fatalf("completed retry: %+v %v", replay, err)
		}
	})
}

func TestCronMutationNewIntentSupersedesOldDeletion(t *testing.T) {
	cronAnchorBackends(t, func(t *testing.T, ctx context.Context, store state.Store, appID, accountID string) {
		c, err := store.CreateCron(ctx, appID, "0 3 * * *", "/original", true)
		if err != nil {
			t.Fatal(err)
		}
		s, old := store.(state.CronMutationStore), reviewedCronRequest(c, true)
		if _, err := s.ApplyCronMutation(ctx, accountID, appID, old); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ApplyCronMutation(ctx, accountID, appID, reviewedCronRequest(c, true)); err != nil {
			t.Fatal(err)
		}
		receipt, err := s.ApplyCronMutation(ctx, accountID, appID, old)
		if err != nil || receipt.Status != "conflict" {
			t.Fatalf("superseded intent advanced: %+v %v", receipt, err)
		}
	})
}

func TestCronMutationExpiryKeepsPassiveReceipt(t *testing.T) {
	cronAnchorBackends(t, func(t *testing.T, ctx context.Context, store state.Store, appID, accountID string) {
		c, err := store.CreateCron(ctx, appID, "0 3 * * *", "/original", true)
		if err != nil {
			t.Fatal(err)
		}
		s, r := store.(state.CronMutationStore), reviewedCronRequest(c, true)
		r.ExpiresAt = time.Now().UTC().Add(500 * time.Millisecond)
		first, err := s.ApplyCronMutation(ctx, accountID, appID, r)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Until(r.ExpiresAt) + time.Millisecond)
		if _, err := s.ApplyCronMutation(ctx, accountID, appID, r); !errors.Is(err, state.ErrCronReviewExpired) {
			t.Fatalf("expired continuation: %v", err)
		}
		read, err := s.CronMutationReceipt(ctx, accountID, appID, r.RequestID, r.ReceiptProof)
		if err != nil || read != first {
			t.Fatalf("expired passive receipt: %+v %v", read, err)
		}
		if _, err := store.CronByID(ctx, c.ID); err != nil {
			t.Fatal("expiry advanced deletion")
		}
	})
}

func TestCronMutationLatePendingRunIsCancelledBeforeDetach(t *testing.T) {
	cronAnchorBackends(t, func(t *testing.T, ctx context.Context, store state.Store, appID, accountID string) {
		c, err := store.CreateCron(ctx, appID, "0 3 * * *", "/original", true)
		if err != nil {
			t.Fatal(err)
		}
		s, r := store.(state.CronMutationStore), reviewedCronRequest(c, true)
		for i := 0; i < 2; i++ {
			if _, err := s.ApplyCronMutation(ctx, accountID, appID, r); err != nil {
				t.Fatal(err)
			}
		}
		// The cancellation phase finished. A scheduler can still enqueue a run
		// before the next transaction obtains the cron's row lock.
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID, Source: state.InvocationCron, CronID: &c.ID, Method: "POST", Path: "/original", DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		var receipt api.CronMutationReceipt
		for i := 0; i < 12; i++ {
			receipt, err = s.ApplyCronMutation(ctx, accountID, appID, r)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Status == "completed" {
				break
			}
		}
		if got, err := store.InvocationByID(ctx, inv.ID); err != nil || got.State != state.InvocationCancelled || receipt.Status != "completed" || receipt.Cancelled != 1 {
			t.Fatalf("late run: %+v receipt=%+v %v", got, receipt, err)
		}
	})
}

func TestCronMutationPostgresConcurrentWriter(t *testing.T) {
	s, pool, ctx := cronPgStore(t)
	appID := seedCronApp(t, ctx, s, "reviewed-concurrent")
	app, err := s.AppByID(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateCron(ctx, appID, "0 3 * * *", "/original", true)
	if err != nil {
		t.Fatal(err)
	}
	r := reviewedCronRequest(c, false)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockCronAccountForApp(ctx, tx, appID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.LockCronMutationApp(ctx, tx, sqlc.LockCronMutationAppParams{AppID: appID, AccountID: app.AccountID}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpdateReviewedCron(ctx, tx, sqlc.UpdateReviewedCronParams{AppID: appID, CronID: c.ID, Path: pgtype.Text{String: "/concurrent", Valid: true}}); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := s.ApplyCronMutation(ctx, app.AccountID, appID, r); result <- err }()
	// Wait until the reviewed mutation is actually waiting on the writer.
	// A preflight read outside this lock would see the old state and later
	// overwrite the concurrent edit; the guarded transaction must reread it.
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var count int
		if err := pool.QueryRow(ctx, "select count(*) from pg_stat_activity where datname=current_database() and wait_event_type='Lock'").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("reviewed mutation did not wait on the concurrent writer")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, state.ErrCronReviewConflict) {
			t.Fatalf("concurrent review: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reviewed mutation did not finish")
	}
	current, err := s.CronByID(ctx, c.ID)
	if err != nil || current.Path != "/concurrent" {
		t.Fatalf("concurrent edit was overwritten: %+v %v", current, err)
	}
}
