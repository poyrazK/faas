//go:build !no_pg

package state_test

// adr: 1198, 1199, 20261024154324

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestRouteInsightsLifecyclePG(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	a, app, _, _ := healthFixture(t, s)
	subscribed := observedHealthNotificationHook(t, s, a, app, "insights", []string{"routes.insight.detected", "routes.insight.resolved"}, true)
	other := observedHealthNotificationHook(t, s, a, app, "other", []string{"app.parked"}, true)
	accountID, appID := pgUUID(t, a.ID), pgUUID(t, app.ID)
	t0 := time.Now().UTC().Truncate(time.Second)
	at := func(d time.Duration) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t0.Add(d), Valid: true} }
	upsert := func(d time.Duration) {
		t.Helper()
		if err := s.UpsertRouteInsight(t.Context(), sqlc.UpsertRouteInsightParams{
			AccountID: accountID, AppID: appID, Kind: "kept_awake", Method: "GET", Route: "/health",
			Message: "GET /health keeps the app awake", Evidence: []byte(`{"awake_share":0.9}`), ObservedAt: at(d),
		}); err != nil {
			t.Fatal(err)
		}
	}
	events := func(hook state.AppWebhook) []string {
		t.Helper()
		rows, err := pool.Query(t.Context(), `SELECT event FROM app_webhook_deliveries WHERE webhook_id = $1 AND event LIKE 'routes.insight.%' ORDER BY (payload->>'transition_id')::timestamptz`, hook.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var e string
			if err := rows.Scan(&e); err != nil {
				t.Fatal(err)
			}
			out = append(out, e)
		}
		return out
	}
	resolve := func(resolvedAt, staleBefore time.Duration) int64 {
		t.Helper()
		n, err := s.ResolveStaleRouteInsights(t.Context(), sqlc.ResolveStaleRouteInsightsParams{ResolvedAt: at(resolvedAt), AppID: appID, StaleBefore: at(staleBefore)})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	upsert(0)
	upsert(6 * time.Hour) // a refresh is not a transition
	if got := events(subscribed); len(got) != 1 || got[0] != "routes.insight.detected" {
		t.Fatalf("after detection: %v", got)
	}
	if got := events(other); len(got) != 0 {
		t.Fatalf("an unsubscribed webhook must not receive insights: %v", got)
	}
	if n := resolve(7*time.Hour, 6*time.Hour); n != 0 {
		t.Fatalf("an insight observed at the stale bound must stay active, resolved %d", n)
	}
	if n := resolve(31*time.Hour, 7*time.Hour); n != 1 {
		t.Fatalf("resolved %d, want 1", n)
	}
	if n := resolve(32*time.Hour, 8*time.Hour); n != 0 {
		t.Fatalf("a resolved insight resolves once, got %d", n)
	}
	upsert(40 * time.Hour) // reopens as a new detection
	if got := events(subscribed); len(got) != 3 || got[1] != "routes.insight.resolved" || got[2] != "routes.insight.detected" {
		t.Fatalf("lifecycle deliveries = %v", got)
	}

	rows, err := s.ListRouteInsights(t.Context(), sqlc.ListRouteInsightsParams{AppID: appID, AccountID: accountID, ResolvedSince: at(-time.Hour), RowLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].State != "active" || !rows[0].FirstDetectedAt.Time.Equal(t0.Add(40*time.Hour)) || rows[0].ResolvedAt.Valid {
		t.Fatalf("rows = %+v", rows)
	}
	if other, err := s.ListRouteInsights(t.Context(), sqlc.ListRouteInsightsParams{AppID: appID, AccountID: pgUUID(t, uuid.NewString()), ResolvedSince: at(-time.Hour), RowLimit: 10}); err != nil || len(other) != 0 {
		t.Fatalf("another account must not see the app's insights: %v %+v", err, other)
	}

	// The CHECKs reject unknown kinds and methods and relative routes.
	for _, bad := range []sqlc.UpsertRouteInsightParams{
		{Kind: "slow", Method: "GET", Route: "/x"},
		{Kind: "cacheable", Method: "PROPFIND", Route: "/x"},
		{Kind: "cacheable", Method: "GET", Route: "x"},
	} {
		bad.AccountID, bad.AppID, bad.Message, bad.Evidence, bad.ObservedAt = accountID, appID, "m", []byte(`{}`), at(0)
		if err := s.UpsertRouteInsight(t.Context(), bad); err == nil {
			t.Fatalf("%+v must violate a CHECK", bad)
		}
	}
	// Cache rule checks (ADR-1199) are insight kinds too.
	for _, kind := range []string{"cache_stale", "cache_too_short"} {
		if err := s.UpsertRouteInsight(t.Context(), sqlc.UpsertRouteInsightParams{
			AccountID: accountID, AppID: appID, Kind: kind, Method: "GET", Route: "/prices",
			Message: "m", Evidence: []byte(`{"refills":50}`), ObservedAt: at(0),
		}); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}

	if _, err := pool.Exec(t.Context(), `INSERT INTO usage_minutes(account_id,app_id,instance_id,minute,mb_seconds) VALUES($1,$2,$3,$4,1000)`,
		a.ID, app.ID, uuid.NewString(), t0.Truncate(time.Minute)); err != nil {
		t.Fatal(err)
	}
	isCandidate := func(plan string, dueBefore time.Duration) bool {
		t.Helper()
		candidates, err := s.RouteInsightCandidateApps(t.Context(), sqlc.RouteInsightCandidateAppsParams{
			SinceAt: at(-time.Hour), Plans: []string{plan}, DueBefore: at(dueBefore), MaxApps: 100,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range candidates {
			if c.AppID == appID && c.AccountID == accountID {
				return true
			}
		}
		return false
	}
	if !isCandidate(string(a.Plan), 0) {
		t.Fatal("an app that ran must be a candidate")
	}

	// adr: 20261024154324 — only apps on the given plans are candidates, a
	// claim holds an app until due_before passes it, and a claimed app is
	// not a candidate meanwhile.
	if isCandidate("no-such-plan", 0) {
		t.Fatal("an app on another plan must not be a candidate")
	}
	claim := func(evaluatedAt, dueBefore time.Duration) int64 {
		t.Helper()
		n, err := s.ClaimRouteInsightEvaluation(t.Context(), sqlc.ClaimRouteInsightEvaluationParams{
			AppID: appID, AccountID: accountID, EvaluatedAt: at(evaluatedAt), DueBefore: at(dueBefore),
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := claim(time.Minute, 0); n != 1 {
		t.Fatalf("first claim = %d, want 1", n)
	}
	if n := claim(2*time.Minute, 0); n != 0 {
		t.Fatalf("a second claim before the app is due = %d, want 0", n)
	}
	if isCandidate(string(a.Plan), 0) {
		t.Fatal("a claimed app must not be a candidate until it is due")
	}
	if !isCandidate(string(a.Plan), 2*time.Minute) || claim(3*time.Minute, 2*time.Minute) != 1 {
		t.Fatal("a due app must be a candidate and claimable again")
	}

	// The batch resolve covers every app's stale insights, row_limit at a
	// time: the reopened kept_awake insight and the two cache rule insights.
	resolveBatch := func(limit int32) int64 {
		t.Helper()
		n, err := s.ResolveStaleRouteInsightsBatch(t.Context(), sqlc.ResolveStaleRouteInsightsBatchParams{
			ResolvedAt: at(100 * time.Hour), StaleBefore: at(50 * time.Hour), RowLimit: limit,
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := []int64{resolveBatch(1), resolveBatch(10), resolveBatch(10)}; got[0] != 1 || got[1] != 2 || got[2] != 0 {
		t.Fatalf("batch resolves = %v, want [1 2 0]", got)
	}
}
