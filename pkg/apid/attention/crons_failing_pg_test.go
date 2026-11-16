package attention

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cronRuns records finished runs of a new cron on app, oldest first, ten
// minutes apart and ending ten minutes ago. Each entry is state/outcome.
func (f fixture) cronRuns(app, path string, enabled bool, command string, runs ...[2]string) {
	f.t.Helper()
	var id string
	if err := f.pool.QueryRow(f.t.Context(), "INSERT INTO crons(app_id, schedule, path, enabled, command) VALUES ($1, '*/15 * * * *', $2, $3, $4::text[]) RETURNING id::text",
		app, path, enabled, command).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	for i, run := range runs {
		at := f.now.Add(-time.Duration(len(runs)-i) * 10 * time.Minute)
		f.exec("INSERT INTO invocations(cron_id, source, state, outcome, created_at, completed_at) VALUES ($1, 'cron', $2, $3, $4, $4)",
			id, run[0], run[1], at)
	}
}

// prod 2026-11-15: an HTTP cron 404'd every 15 minutes for days, waking and
// billing the app, and nothing told the customer.
func TestEvaluateCronsFailing(t *testing.T) {
	e, pool := pgEvaluator(t)
	now := time.Now().UTC().Truncate(time.Second)
	e.Now = func() time.Time { return now }
	f := fixture{t: t, pool: pool, now: now}
	a := f.account()
	shop := f.app(a, "shop")
	failed, timeout, ok := [2]string{"failed", "failed"}, [2]string{"failed", "timeout"}, [2]string{"completed", "success"}
	streak := make([][2]string, 0, api.AttentionCronFailures)
	for i := 0; i < api.AttentionCronFailures; i++ {
		streak = append(streak, failed)
	}
	streak[1] = timeout
	f.cronRuns(shop, "/sync", true, "{}", append([][2]string{ok}, streak...)...)
	f.cronRuns(shop, "/recovered", true, "{}", append(append([][2]string{}, streak...), ok)...)
	f.cronRuns(shop, "/off", false, "{}", streak...)
	f.cronRuns(shop, "/cmd", true, "{echo}", streak...)
	f.cronRuns(shop, "/short", true, "{}", streak[1:]...)

	if _, err := e.Evaluate(t.Context()); err != nil {
		t.Fatal(err)
	}
	signals, err := e.List(t.Context(), Filter{Kind: api.AttentionKindCronsFailing})
	if err != nil {
		t.Fatal(err)
	}
	if len(signals) != 1 {
		t.Fatalf("crons_failing signals = %+v, want only shop /sync", signals)
	}
	if s := signals[0]; s.AccountID != a || s.Subject != "shop /sync" || s.Detail != "last 5 runs of */15 * * * * /sync failed (failed, timeout)" {
		t.Fatalf("signal = %+v", s)
	}
}
