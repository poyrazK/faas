package state_test

// adr: 955

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type adviceRow struct {
	app, consumer      string
	method, route      string
	status, count, lat int
	cold               bool
	outcome            string
	at                 time.Time
}

func insertAdviceTelemetry(t *testing.T, pool *pgxpool.Pool, account, deployment string, rows []adviceRow) {
	t.Helper()
	for _, r := range rows {
		var consumer any
		if r.consumer != "" {
			consumer = r.consumer
		}
		outcome := r.outcome
		if outcome == "" {
			outcome = "ok"
		}
		productionMonitorExec(t, pool, `INSERT INTO request_telemetry(account_id,app_id,deployment_id,method,route,status,latency_ms,cold_boot,guest_outcome,count,consumer_id,received_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			account, r.app, deployment, r.method, r.route, r.status, r.lat, r.cold, outcome, r.count, consumer, r.at)
	}
}

func insertAdviceConsumer(t *testing.T, pool *pgxpool.Pool, account, app, ref string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), `INSERT INTO api_consumers(account_id,app_id,external_ref,name) VALUES($1,$2,$3,$3) RETURNING id::text`, account, app, ref).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRouteAdviceQueriesPG(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	a, app, _, d := healthFixture(t, s)
	_, other, _, _ := healthFixture(t, s)
	now := time.Now().UTC()
	base := now.Truncate(time.Minute).Add(-30 * time.Minute)
	c1 := insertAdviceConsumer(t, pool, a.ID, app.ID, "c1")
	c2 := insertAdviceConsumer(t, pool, a.ID, app.ID, "c2")
	c3 := insertAdviceConsumer(t, pool, a.ID, app.ID, "c3")
	foreign := insertAdviceConsumer(t, pool, a.ID, other.ID, "foreign")
	get := func(route string, status, count int, cold bool, at time.Time, consumer string) adviceRow {
		return adviceRow{app: app.ID, consumer: consumer, method: "GET", route: "GET " + route, status: status, count: count, lat: 20, cold: cold, at: at}
	}
	insertAdviceTelemetry(t, pool, a.ID, d.ID, []adviceRow{
		// Cache: one 60 s window holds 5 anonymous successes (4 hits, one of
		// them a cold boot the cache avoids); the next window holds 2 (1 hit).
		get("/catalog", 200, 1, false, base, ""),
		get("/catalog", 200, 3, false, base.Add(10*time.Second), ""),
		get("/catalog", 200, 1, true, base.Add(20*time.Second), ""),
		get("/catalog", 200, 2, false, base.Add(70*time.Second), ""),
		get("/catalog", 200, 1, false, base.Add(30*time.Second), c1), // identified: no cache estimate
		get("/catalog", 503, 1, false, base.Add(40*time.Second), ""),
		{app: app.ID, method: "POST", route: "POST /render", status: 504, count: 2, lat: 30000, outcome: "timeout", at: base},
		{app: app.ID, method: "POST", route: "POST /render", status: 200, count: 5, lat: 900, at: base},
		{app: app.ID, method: "GET", route: "__route_other__", status: 200, count: 50, lat: 1, at: base},
		// Consumers on /search: c1 peaks at 30 in one minute over two rows.
		get("/search", 200, 20, false, base, c1),
		get("/search", 200, 10, false, base.Add(30*time.Second), c1),
		get("/search", 200, 5, false, base.Add(2*time.Minute), c1),
		get("/search", 200, 4, false, base, c2),
		get("/search", 200, 1, false, base, c3),
		get("/search", 200, 100, false, base, foreign),                 // another app's consumer: ignored
		get("/catalog", 200, 100, false, now.Add(-3*time.Hour), ""),    // outside the window
		{app: other.ID, method: "GET", route: "GET /catalog", status: 200, count: 100, lat: 1, at: base}, // another app
	})
	q := sqlc.New()
	appID, accountID := mustPgUUID(t, app.ID), mustPgUUID(t, a.ID)
	since, until := pgtype.Timestamptz{Time: now.Add(-2 * time.Hour), Valid: true}, pgtype.Timestamptz{Time: now.Add(time.Minute), Valid: true}
	stats, err := q.RouteAdviceRouteStats(t.Context(), pool, sqlc.RouteAdviceRouteStatsParams{AppID: appID, AccountID: accountID, SinceAt: since, UntilAt: until, RouteLimit: 10, CacheMaxAgeSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	byRoute := map[string]sqlc.RouteAdviceRouteStatsRow{}
	for _, r := range stats {
		byRoute[r.Route] = r
	}
	if len(stats) != 3 || stats[0].Route != "GET /search" {
		t.Fatalf("want 3 routes ordered by volume without __route_other__, got %+v", stats)
	}
	catalog := byRoute["GET /catalog"]
	if catalog.Requests != 9 || catalog.AnonymousRequests != 8 || catalog.AnonymousSuccess != 7 || catalog.ServerErrors != 1 ||
		catalog.ColdBoots != 1 || catalog.CacheHits != 5 || catalog.WakesAvoided != 1 {
		t.Fatalf("catalog = %+v", catalog)
	}
	render := byRoute["POST /render"]
	if render.Requests != 7 || render.Timeouts != 2 || render.CacheHits != 0 {
		t.Fatalf("render = %+v", render)
	}
	consumers, err := q.RouteAdviceConsumers(t.Context(), pool, sqlc.RouteAdviceConsumersParams{AppID: appID, AccountID: accountID, SinceAt: since, UntilAt: until})
	if err != nil {
		t.Fatal(err)
	}
	var search []sqlc.RouteAdviceConsumersRow
	for _, c := range consumers {
		if c.Route == "GET /search" {
			search = append(search, c)
		}
	}
	if len(search) != 2 || pgUUIDText(search[0].ConsumerID) != c1 || search[0].Requests != 35 || search[0].PeakPerMinute != 30 || search[0].Consumers != 3 ||
		pgUUIDText(search[1].ConsumerID) != c2 || search[1].PeakPerMinute != 4 {
		t.Fatalf("search consumers = %+v", search)
	}
	excess, err := q.RouteAdviceThrottleExcess(t.Context(), pool, sqlc.RouteAdviceThrottleExcessParams{
		AllowancePerMinute: 10, AppID: appID, AccountID: accountID, ConsumerID: mustPgUUID(t, c1),
		Route: "GET /search", Method: "GET", SinceAt: since, UntilAt: until,
	})
	if err != nil || excess != 20 {
		t.Fatalf("excess = %d, %v; want 20", excess, err)
	}
	stats, err = q.RouteAdviceRouteStats(t.Context(), pool, sqlc.RouteAdviceRouteStatsParams{AppID: appID, AccountID: accountID, SinceAt: since, UntilAt: until, RouteLimit: 1, CacheMaxAgeSeconds: 60})
	if err != nil || len(stats) != 1 || stats[0].Route != "GET /search" {
		t.Fatalf("route limit not applied: %+v %v", stats, err)
	}
}

func pgUUIDText(u pgtype.UUID) string {
	v, _ := u.Value()
	s, _ := v.(string)
	return s
}
