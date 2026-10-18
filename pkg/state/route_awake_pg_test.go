//go:build !no_pg

package state_test

// adr: 1187

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const nilDeployment = "00000000-0000-0000-0000-000000000000"

type awakeTelemetry struct {
	app, deployment, instance, consumer string
	method, route                       string
	status, count                       int
	cold                                bool
	at                                  time.Time
}

func insertAwakeUsage(t *testing.T, pool *pgxpool.Pool, account, app, instance string, at time.Time, mbs int64, cold int) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `INSERT INTO usage_minutes(account_id,app_id,instance_id,minute,mb_seconds,cold_boot_count) VALUES($1,$2,$3,$4,$5,$6)`,
		account, app, instance, at, mbs, cold); err != nil {
		t.Fatal(err)
	}
}

func insertAwakeTelemetry(t *testing.T, pool *pgxpool.Pool, account string, rows []awakeTelemetry) {
	t.Helper()
	for _, r := range rows {
		var consumer, instance any
		if r.consumer != "" {
			consumer = r.consumer
		}
		if r.instance != "" {
			instance = r.instance
		}
		if _, err := pool.Exec(t.Context(), `INSERT INTO request_telemetry(account_id,app_id,deployment_id,instance_id,method,route,status,latency_ms,cold_boot,guest_outcome,count,consumer_id,received_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,20,$8,'ok',$9,$10,$11)`,
			account, r.app, r.deployment, instance, r.method, r.route, r.status, r.cold, r.count, consumer, r.at); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRouteAwakeQueriesPG(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	a, app, _, d := healthFixture(t, s)
	b, other, _, od := healthFixture(t, s)
	base := time.Now().UTC().Truncate(time.Minute).Add(-30 * time.Minute)
	minute := func(n int) time.Time { return base.Add(time.Duration(n) * time.Minute) }
	inst1, inst2 := uuid.NewString(), uuid.NewString()
	insertAwakeUsage(t, pool, a.ID, app.ID, inst1, minute(0), 15840, 1)
	insertAwakeUsage(t, pool, a.ID, app.ID, inst2, minute(0), 1000, 0) // summed with inst1
	insertAwakeUsage(t, pool, a.ID, app.ID, inst1, minute(1), 15840, 0)
	insertAwakeUsage(t, pool, a.ID, app.ID, inst2, minute(2), 0, 0) // no running time: dropped
	insertAwakeUsage(t, pool, b.ID, other.ID, uuid.NewString(), minute(0), 999, 0)

	accountID, appID := pgUUID(t, a.ID), pgUUID(t, app.ID)
	since, until := pgtype.Timestamptz{Time: minute(-1), Valid: true}, pgtype.Timestamptz{Time: minute(10), Valid: true}
	usage, err := s.RouteAwakeMinutes(t.Context(), sqlc.RouteAwakeMinutesParams{AccountID: accountID, AppID: appID, SinceAt: since, UntilAt: until})
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 2 || !usage[0].Minute.Time.Equal(minute(0)) || usage[0].MbSeconds != 16840 || usage[0].ColdBoots != 1 || usage[1].MbSeconds != 15840 {
		t.Fatalf("usage = %+v", usage)
	}

	consumer := insertAdviceConsumer(t, pool, a.ID, app.ID, "poller")
	call := func(method, route string, status, count int, cold bool, at time.Time) awakeTelemetry {
		return awakeTelemetry{app: app.ID, deployment: d.ID, instance: inst1, method: method, route: method + " " + route, status: status, count: count, cold: cold, at: at}
	}
	identified := call("GET", "/health", 200, 1, false, minute(0).Add(30*time.Second))
	identified.consumer = consumer
	cached := call("GET", "/cached", 200, 9, false, minute(0))
	cached.deployment, cached.instance = nilDeployment, ""
	spread := call("GET", "/x", 200, 2, false, minute(1))
	spread.instance = "" // collapsed across instances: still reached the app
	foreign := awakeTelemetry{app: other.ID, deployment: od.ID, method: "GET", route: "GET /health", status: 200, count: 7, at: minute(0)}
	insertAwakeTelemetry(t, pool, a.ID, []awakeTelemetry{
		call("GET", "/health", 200, 3, false, minute(0).Add(5*time.Second)),
		identified,
		call("GET", "/health", 200, 2, false, minute(1).Add(10*time.Second)),
		call("GET", "/health", 401, 5, false, minute(0)), // errors do not restart the idle timer
		call("POST", "/orders", 500, 1, true, minute(0)), // but a cold boot starts it
		cached,
		spread,
	})
	insertAwakeTelemetry(t, pool, b.ID, []awakeTelemetry{foreign})
	rows, err := s.RouteAwakeRouteMinutes(t.Context(), sqlc.RouteAwakeRouteMinutesParams{AccountID: accountID, AppID: appID, SinceAt: since, UntilAt: until, RouteLimit: 2})
	if err != nil {
		t.Fatal(err)
	}
	want := []sqlc.RouteAwakeRouteMinutesRow{
		{Route: "", Method: "", Requests: 1, ColdBoots: 1, AnonymousRequests: 1}, // POST /orders folds into other routes
		{Route: "GET /health", Method: "GET", Requests: 4, AnonymousRequests: 3},
		{Route: "GET /health", Method: "GET", Requests: 2, AnonymousRequests: 2},
		{Route: "GET /x", Method: "GET", Requests: 2, AnonymousRequests: 2},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v", rows)
	}
	for i, w := range want {
		got := rows[i]
		w.Minute = got.Minute
		if got != w {
			t.Fatalf("row %d = %+v, want %+v", i, got, w)
		}
	}
	if !rows[0].Minute.Time.Equal(minute(0)) || !rows[2].Minute.Time.Equal(minute(1)) {
		t.Fatalf("minutes = %v %v", rows[0].Minute.Time, rows[2].Minute.Time)
	}
}
