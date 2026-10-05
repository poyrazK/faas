package state_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestRouteLatencyWeightedEvidenceAndAtomicGate(t *testing.T) {
	for _, scenario := range []string{"weighted_healthy", "budget_exceeded", "relative_only", "mixed_signals", "sparse", "worker", "report", "budget_changed"} {
		t.Run(scenario, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			if err := db.MigrateUp(t.Context(), pool); err != nil {
				t.Fatal(err)
			}
			s := state.NewPgStore(pool)
			a, app, stable, d := healthFixture(t, s)
			if _, err := pool.Exec(t.Context(), "UPDATE deployments SET canary_step_started_at = clock_timestamp() - interval '1 hour', rollout_state = 'rolling_out' WHERE id = $1", d.ID); err != nil {
				t.Fatal(err)
			}
			zero := int64(0)
			mode := "enforce"
			if scenario == "report" {
				mode = "report"
			}
			selectors := []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", MaxP95MS: 200}, {Method: "GET", Path: "/busy"}}
			if scenario == "relative_only" {
				selectors[0].CheckLatency = true
				selectors[0].MaxP95MS = 0
			}
			g, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: mode, ExpectedRevision: &zero, Routes: selectors})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), "UPDATE route_health_gates SET updated_at = clock_timestamp() - interval '1 hour' WHERE app_id = $1", app.ID); err != nil {
				t.Fatal(err)
			}
			windows := routehealth.Windows(time.Now().UTC())
			// The report and atomic gate read the DB clock separately. Keep the
			// following minute populated if their closed-window cutoff rolls over.
			nextStart := windows[len(windows)-1].End
			windows = append(windows, api.RouteHealthWindowEvidence{Start: nextStart, End: nextStart.Add(api.RouteHealthWindow)})
			q := &sqlc.Queries{}
			for i, w := range windows {
				for _, dep := range []state.Deployment{stable, d} {
					for _, route := range selectors {
						fast, slow, total := int32(95), int32(5), int32(100)
						fastMS, slowMS := int32(100), int32(100)
						status := int32(200)
						if dep.ID == d.ID && route.Path == "/checkout" {
							slowMS = 1000 // weighted p95=145, not the unweighted two-row p95=955
							if scenario != "weighted_healthy" && scenario != "worker" && scenario != "budget_changed" {
								fastMS = 850
								slowMS = 1000
							}
							if scenario == "sparse" {
								total = 99
								fast = 94
							}
							if scenario == "mixed_signals" && i == 1 {
								fastMS = 100
								slowMS = 100
								status = 500
							}
						}
						if route.Path == "/busy" {
							fast = total
							slow = 0
						}
						for _, row := range []struct{ count, latency int32 }{{fast, fastMS}, {slow, slowMS}} {
							if row.count == 0 {
								continue
							}
							p := sqlc.InsertRequestTelemetryParams{AccountID: pgtype.UUID{Bytes: uuid.MustParse(a.ID), Valid: true}, AppID: pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}, DeploymentID: pgtype.UUID{Bytes: uuid.MustParse(dep.ID), Valid: true}, Route: route.Method + " " + route.Path, Method: route.Method, Status: status, LatencyMs: row.latency, ReceivedAt: state.NewPgtypeTime(w.Start.Add(time.Second)), Count: row.count, UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", GuestRuntime: "__unknown__", GuestOutcome: "missing", FlagEvidenceJson: "[]"}
							if err := q.InsertRequestTelemetry(t.Context(), pool, p); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
			}
			if scenario == "budget_changed" {
				selectors[0].MaxP95MS = 600
				updated, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: mode, ExpectedRevision: &g.Revision, Routes: selectors})
				if err != nil || updated.Revision != g.Revision+1 {
					t.Fatal("budget edit did not reset revision")
				}
			}
			report, err := s.GetRouteHealthReport(t.Context(), a.ID, app.ID, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "regressed"
			if scenario == "weighted_healthy" || scenario == "worker" {
				want = "healthy"
			}
			if scenario == "mixed_signals" || scenario == "sparse" || scenario == "budget_changed" {
				want = "unknown"
			}
			if report.Status != want || report.MinimumLatencyRequests != 100 || report.Routes[0].MaxP95MS != selectors[0].MaxP95MS {
				t.Fatalf("%s: %+v", scenario, report)
			}
			p95 := report.Routes[0].Windows[0].Candidate.P95LatencyMS
			if p95 == nil {
				t.Fatal("percentile missing")
			}
			if scenario == "weighted_healthy" || scenario == "worker" || scenario == "budget_changed" {
				if math.Abs(*p95-145) > 1e-9 {
					t.Fatalf("unweighted or uninterpolated p95: %f", *p95)
				}
			}
			if report.Routes[1].Windows[0].Candidate.P95LatencyMS != nil {
				t.Fatal("legacy selector unnecessarily computed p95")
			}
			var decision api.RouteHealthDecision
			params := state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteHealthDecision: &decision, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "latency-test"}}
			if scenario == "worker" {
				if err := s.StampSafeReleaseWorkerLease(t.Context(), time.Minute); err != nil {
					t.Fatal(err)
				}
				params.RequireSafeReleaseLease = true
				params.RequireCanaryStageElapsed = true
				params.CanaryStageDuration = time.Minute
			}
			updated, auditID, err := s.AdvanceCanary(t.Context(), d.ID, params)
			if want == "healthy" || scenario == "report" {
				if err != nil || auditID == 0 || updated.TrafficPercent != 10 {
					t.Fatalf("allowed %+v %v", decision, err)
				}
			} else {
				var blocked *state.RouteHealthBlockedError
				if !errors.As(err, &blocked) || decision.Status != "blocked" {
					t.Fatalf("failed open: %+v %v", decision, err)
				}
				current, _ := s.DeploymentByID(t.Context(), d.ID)
				if current.CanaryStep != 0 || current.TrafficPercent != 1 {
					t.Fatal("latency hold wrote traffic")
				}
				audits, err := s.ListDeploymentAudit(t.Context(), d.ID, 10)
				if err != nil || len(audits) != 0 {
					t.Fatal("latency hold wrote traffic audit")
				}
			}
		})
	}
}
