package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// adr: 846
// A route with about five candidate requests per minute never fills a
// one-minute window, but two halves of a 12-minute stage reach a verdict.
func TestRouteHealthPostgresPoolsSparseStageEvidence(t *testing.T) {
	for _, scenario := range []string{"healthy", "regressed"} {
		t.Run(scenario, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			if err := db.MigrateUp(t.Context(), pool); err != nil {
				t.Fatal(err)
			}
			s := state.NewPgStore(pool)
			a, app, stable, d := healthFixture(t, s)
			if _, err := pool.Exec(t.Context(), "UPDATE deployments SET canary_step_started_at = clock_timestamp() - interval '12 minutes', rollout_state = 'rolling_out' WHERE id = $1", d.ID); err != nil {
				t.Fatal(err)
			}
			zero := int64(0)
			if _, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/refund"}}}); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), "UPDATE route_health_gates SET updated_at = clock_timestamp() - interval '1 hour' WHERE app_id = $1", app.ID); err != nil {
				t.Fatal(err)
			}
			q := &sqlc.Queries{}
			now := time.Now().UTC()
			for minute := 1; minute <= 12; minute++ {
				at := now.Add(-time.Duration(minute) * time.Minute)
				for _, dep := range []state.Deployment{stable, d} {
					total, errorsCount := int32(50), int32(0)
					if dep.ID == d.ID {
						total = 5
						if scenario == "regressed" {
							errorsCount = 1
						}
					}
					for _, entry := range []struct{ status, count int32 }{{200, total - errorsCount}, {500, errorsCount}} {
						if entry.count == 0 {
							continue
						}
						params := sqlc.InsertRequestTelemetryParams{AccountID: pgtype.UUID{Bytes: uuid.MustParse(a.ID), Valid: true}, AppID: pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}, DeploymentID: pgtype.UUID{Bytes: uuid.MustParse(dep.ID), Valid: true}, Route: "POST /refund", Method: "POST", Status: entry.status, LatencyMs: 10, ReceivedAt: state.NewPgtypeTime(at), Count: entry.count, UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", GuestRuntime: "__unknown__", GuestOutcome: "missing", FlagEvidenceJson: "[]"}
						if err := q.InsertRequestTelemetry(t.Context(), pool, params); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			r, err := s.GetRouteHealthReport(t.Context(), a.ID, app.ID, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			f := r.Routes[0]
			if f.Status != scenario || f.EvidenceWindow != "pooled" || r.Status != scenario {
				t.Fatalf("pooled %s finding = %+v (report %s)", scenario, f, r.Status)
			}
			if len(f.Windows) != api.RouteHealthWindows || f.Windows[0].End.Sub(f.Windows[0].Start) != api.RouteHealthWindow {
				t.Fatalf("one-minute windows were replaced: %+v", f.Windows)
			}
			span := f.PooledWindows[1].End.Sub(f.PooledWindows[0].Start)
			if span < api.RouteHealthPooledMinSpan || span > api.RouteHealthPooledMaxSpan || !f.PooledWindows[0].End.Equal(f.PooledWindows[1].Start) {
				t.Fatalf("pooled windows = %+v", f.PooledWindows)
			}
			for _, w := range f.PooledWindows {
				if w.Candidate.Requests < api.RouteHealthMinRequests || w.Stable.Requests < api.RouteHealthMinRequests {
					t.Fatalf("pooled window lacks requests: %+v", w)
				}
			}
		})
	}
}
