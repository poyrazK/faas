package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// cronHistoryBackend seeds and inspects cron history rows without going
// through the scheduler, so a test can place them at any age.
type cronHistoryBackend struct {
	store      state.CronHistoryRetentionStore
	occurrence func(t *testing.T, id, cronID, accountID, status, blocking string, at time.Time)
	request    func(t *testing.T, id, cronID, accountID string, at time.Time)
	// occurrenceBlocking reports whether the occurrence exists and the id it
	// is blocked by.
	occurrenceBlocking func(t *testing.T, id string) (bool, string)
	requestExists      func(t *testing.T, id string) bool
}

func cronHistoryBackends(t *testing.T, fn func(t *testing.T, ctx context.Context, b cronHistoryBackend, cronID, accountID string)) {
	t.Run("memory", func(t *testing.T) {
		s := state.NewMemStore()
		ctx := context.Background()
		acct, err := s.CreateAccount(ctx, "cron-history@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "cron-history", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		c, err := s.CreateCron(ctx, app.ID, "* * * * *", "/tick", true)
		if err != nil {
			t.Fatal(err)
		}
		fn(t, ctx, cronHistoryBackend{
			store: s,
			occurrence: func(_ *testing.T, id, cronID, accountID, status, blocking string, at time.Time) {
				s.SeedScheduleOccurrenceForTest(state.ScheduleOccurrence{ID: id, AccountID: accountID, CronID: cronID,
					ScheduleRevision: 1, ScheduledFor: at, Status: status, BlockingOccurrenceID: blocking})
			},
			request: func(_ *testing.T, id, cronID, accountID string, at time.Time) {
				s.SeedFireNowRequestForTest(state.FireNowRequest{ID: id, CronID: cronID, AccountID: accountID,
					RequestedAt: at, Status: state.FireNowStatusRunning})
			},
			occurrenceBlocking: func(_ *testing.T, id string) (bool, string) {
				o, ok := s.ScheduleOccurrenceForTest(id)
				return ok, o.BlockingOccurrenceID
			},
			requestExists: func(_ *testing.T, id string) bool { return s.FireNowRequestForTest(id) },
		}, c.ID, acct.ID)
	})
	t.Run("postgres", func(t *testing.T) {
		s, pool, ctx := cronPgStore(t)
		appID := seedCronApp(t, ctx, s, "history")
		app, err := s.AppByID(ctx, appID)
		if err != nil {
			t.Fatal(err)
		}
		c, err := s.CreateCron(ctx, appID, "* * * * *", "/tick", true)
		if err != nil {
			t.Fatal(err)
		}
		fn(t, ctx, pgCronHistoryBackend(ctx, s, pool), c.ID, app.AccountID)
	})
}

func pgCronHistoryBackend(ctx context.Context, s *state.PgStore, pool *pgxpool.Pool) cronHistoryBackend {
	return cronHistoryBackend{
		store: s,
		occurrence: func(t *testing.T, id, cronID, accountID, status, blocking string, at time.Time) {
			t.Helper()
			if _, err := pool.Exec(ctx, `insert into schedule_occurrences
				(id, account_id, cron_id, schedule_revision, scheduled_for, schedule_policy, status, blocking_occurrence_id)
				values ($1::uuid, $2::uuid, $3::uuid, 1, $4, '{"version":1}'::jsonb, $5, nullif($6, '')::uuid)`,
				id, accountID, cronID, at, status, blocking); err != nil {
				t.Fatalf("seed occurrence: %v", err)
			}
		},
		request: func(t *testing.T, id, cronID, accountID string, at time.Time) {
			t.Helper()
			if _, err := pool.Exec(ctx, `insert into cron_fire_now_requests (id, cron_id, account_id, requested_at, status)
				values ($1::uuid, $2::uuid, $3::uuid, $4, 'running')`, id, cronID, accountID, at); err != nil {
				t.Fatalf("seed manual-run request: %v", err)
			}
		},
		occurrenceBlocking: func(t *testing.T, id string) (bool, string) {
			t.Helper()
			var blocking *string
			err := pool.QueryRow(ctx, `select blocking_occurrence_id::text from schedule_occurrences where id = $1::uuid`, id).Scan(&blocking)
			if errors.Is(err, pgx.ErrNoRows) {
				return false, ""
			}
			if err != nil {
				t.Fatalf("read occurrence: %v", err)
			}
			if blocking == nil {
				return true, ""
			}
			return true, *blocking
		},
		requestExists: func(t *testing.T, id string) bool {
			t.Helper()
			var n int
			if err := pool.QueryRow(ctx, `select count(*) from cron_fire_now_requests where id = $1::uuid`, id).Scan(&n); err != nil {
				t.Fatalf("read manual-run request: %v", err)
			}
			return n == 1
		},
	}
}

func TestPruneCronHistory(t *testing.T) {
	cronHistoryBackends(t, func(t *testing.T, ctx context.Context, b cronHistoryBackend, cronID, accountID string) {
		now := time.Now().UTC().Truncate(time.Second)
		before := now.Add(-api.CronHistoryRetention)
		old, recent := before.Add(-24*time.Hour), now.Add(-time.Hour)
		ids := map[string]string{}
		for _, name := range []string{"oldSucceeded", "oldFailed", "oldQueued", "oldUncertain", "recentSkipped", "oldRequest", "recentRequest"} {
			ids[name] = uuid.NewString()
		}
		b.occurrence(t, ids["oldSucceeded"], cronID, accountID, "succeeded", "", old)
		b.occurrence(t, ids["oldFailed"], cronID, accountID, "failed", "", old.Add(time.Minute))
		b.occurrence(t, ids["oldQueued"], cronID, accountID, "queued", "", old.Add(2*time.Minute))
		b.occurrence(t, ids["oldUncertain"], cronID, accountID, "uncertain", "", old.Add(3*time.Minute))
		b.occurrence(t, ids["recentSkipped"], cronID, accountID, "skipped_overlap", ids["oldSucceeded"], recent)
		b.request(t, ids["oldRequest"], cronID, accountID, old)
		b.request(t, ids["recentRequest"], cronID, accountID, recent)

		if _, err := b.store.PruneCronHistory(ctx, before, api.CronHistoryPruneBatch+1); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("oversized batch: err = %v, want ErrInvalidArgument", err)
		}
		// A batch of one takes the oldest settled decision and the oldest request.
		n, err := b.store.PruneCronHistory(ctx, before, 1)
		if err != nil || n != 2 {
			t.Fatalf("first batch: n=%d err=%v, want 2 rows", n, err)
		}
		if ok, _ := b.occurrenceBlocking(t, ids["oldSucceeded"]); ok {
			t.Fatal("the oldest settled decision survived the first batch")
		}
		if ok, _ := b.occurrenceBlocking(t, ids["oldFailed"]); !ok {
			t.Fatal("a batch of one deleted more than one decision")
		}
		if n, err = b.store.PruneCronHistory(ctx, before, api.CronHistoryPruneBatch); err != nil || n != 1 {
			t.Fatalf("second batch: n=%d err=%v, want the remaining settled decision", n, err)
		}
		if n, err = b.store.PruneCronHistory(ctx, before, api.CronHistoryPruneBatch); err != nil || n != 0 {
			t.Fatalf("third batch: n=%d err=%v, want nothing left to prune", n, err)
		}
		for _, name := range []string{"oldQueued", "oldUncertain"} {
			if ok, _ := b.occurrenceBlocking(t, ids[name]); !ok {
				t.Fatalf("%s was pruned: unsettled decisions are kept whatever their age", name)
			}
		}
		ok, blocking := b.occurrenceBlocking(t, ids["recentSkipped"])
		if !ok || blocking != "" {
			t.Fatalf("recent decision: exists=%v blocking=%q, want kept with its deleted blocker cleared", ok, blocking)
		}
		if b.requestExists(t, ids["oldRequest"]) || !b.requestExists(t, ids["recentRequest"]) {
			t.Fatal("manual-run requests: want the old one pruned and the recent one kept")
		}
	})
}

func TestCronHistoryRetentionCoversEveryPlansRunHistory(t *testing.T) {
	for _, plan := range api.Plans {
		limits := api.MustLimitsFor(plan)
		if retention := time.Duration(limits.MaxAsyncResultRetentionSeconds) * time.Second; retention > api.CronHistoryRetention {
			t.Errorf("plan %s keeps cron runs for %v, longer than the %v cron history retention", plan, retention, api.CronHistoryRetention)
		}
	}
}
