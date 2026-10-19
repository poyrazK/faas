//go:build !no_pg

package state_test

// adr: 1188, 1199

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

func TestRouteAdviceContentEvidencePG(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	a, app, _, d := healthFixture(t, s)
	base := time.Now().UTC().Truncate(time.Minute).Add(-30 * time.Minute)
	insert := func(outcome string, count int) {
		t.Helper()
		productionMonitorExec(t, pool, `INSERT INTO request_telemetry(account_id,app_id,deployment_id,method,route,status,latency_ms,guest_outcome,count,content_outcome,received_at)
			VALUES($1,$2,$3,'GET','GET /catalog',200,10,'ok',$4,$5,$6)`, a.ID, app.ID, d.ID, count, outcome, base)
	}
	insert("first", 10)
	insert("same_60", 300)
	insert("same_300", 200)
	insert("changed_300", 1)
	insert("uncacheable", 5)
	insert("stale", 7) // a cache rule refill whose body changed (ADR-1199)
	insert("", 40)     // not observed
	// The receiver's insert path persists the outcome.
	accountID, appID, deploymentID := pgUUID(t, a.ID), pgUUID(t, app.ID), pgUUID(t, d.ID)
	if err := s.InsertRequestTelemetryWithLogEvent(t.Context(), sqlc.InsertRequestTelemetryParams{
		AccountID: accountID, AppID: appID, DeploymentID: deploymentID, Route: "GET /catalog", Method: "GET",
		Status: 200, LatencyMs: 10, ReceivedAt: pgtype.Timestamptz{Time: base, Valid: true}, Count: 100,
		UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__",
		ContentOutcome: "same_3600",
	}, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO request_telemetry(account_id,app_id,deployment_id,method,route,status,latency_ms,content_outcome)
		VALUES($1,$2,$3,'GET','GET /catalog',200,10,'bogus')`, a.ID, app.ID, d.ID); err == nil {
		t.Fatal("an unknown content outcome must violate the CHECK")
	}
	rows, err := s.RouteAdviceRouteStats(t.Context(), sqlc.RouteAdviceRouteStatsParams{
		AppID: appID, AccountID: accountID,
		SinceAt: pgtype.Timestamptz{Time: base.Add(-time.Hour), Valid: true}, UntilAt: pgtype.Timestamptz{Time: base.Add(time.Hour), Valid: true},
		RouteLimit: 10, CacheMaxAgeSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	r := rows[0]
	if r.Requests != 663 || r.ContentFirst != 10 || r.ContentUncacheable != 5 || r.ContentSame60 != 300 || r.ContentSame300 != 200 ||
		r.ContentSame3600 != 100 || r.ContentChanged60 != 0 || r.ContentChanged300 != 1 || r.ContentChanged3600 != 0 || r.ContentStale != 7 {
		t.Fatalf("content stats = %+v", r)
	}
}
