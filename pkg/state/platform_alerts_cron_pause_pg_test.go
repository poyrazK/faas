//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A */15 cron failing for 25 h pauses; one failing for 3 h, or with a
// success in its newest runs, does not (async assessment 2026-11-28: the
// newest-10 window could never pause a cron firing more often than 2.4 h).
func TestPgPauseCronIfFailingCountsTheWholeStreak(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failures int
		success  int // generate_series index of a success, 0 for none
		want     bool
	}{
		{name: "25 h of failures", failures: 100, want: true},
		{name: "3 h of failures", failures: 12, want: false},
		{name: "success 2 h ago", failures: 100, success: 8, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, pool, ctx := pgStoreWithPool(t)
			acct, err := store.CreateAccount(ctx, "pause-pg@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "pause-pg"})
			if err != nil {
				t.Fatal(err)
			}
			cron, err := store.CreateCron(ctx, app.ID, "*/15 * * * *", "/tick", true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `
				insert into invocations (app_id, account_id, source, method, path, cron_id, state, last_error,
				                         due_at, created_at, received_at, completed_at)
				select $1::uuid, $2::uuid, 'cron', 'POST', '/tick', $3::uuid,
				       case when g = $5::int then 'completed' else 'failed' end,
				       case when g = $5::int then null else 'HTTP 503' end,
				       now() - g * interval '15 minutes', now() - g * interval '15 minutes',
				       now() - g * interval '15 minutes', now() - g * interval '15 minutes'
				  from generate_series(1, $4::int) g`, app.ID, acct.ID, cron.ID, tc.failures, tc.success); err != nil {
				t.Fatal(err)
			}
			got, err := store.PauseCronIfFailing(ctx, cron.ID, api.CronFailurePauseRuns, api.CronFailurePauseSpan, api.PlatformAlertCooldown)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("paused = %v, want %v", got, tc.want)
			}
		})
	}
}
