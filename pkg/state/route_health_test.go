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
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func healthFixture(t *testing.T, s state.Store) (state.Account, state.App, state.Deployment, state.Deployment) {
	t.Helper()
	account, err := s.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "health-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	stable, err := s.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), Kind: state.DeploymentKindImage, AppID: app.ID, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(t.Context(), stable.ID); err != nil {
		t.Fatal(err)
	}
	candidate, err := s.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), Kind: state.DeploymentKindImage, AppID: app.ID, ImageDigest: "sha256:candidate", CanaryPreset: "balanced", CanaryTotalSteps: 4, RolloutState: "pending", TrafficPercent: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(t.Context(), candidate.ID); err != nil {
		t.Fatal(err)
	}
	if mem, ok := s.(*state.MemStore); ok {
		if err := mem.SetDeploymentCanaryState(t.Context(), candidate.ID, "balanced", 0, 4, time.Now().Add(-time.Hour), "rolling_out"); err != nil {
			t.Fatal(err)
		}
	}
	return account, app, stable, candidate
}
func TestRouteHealthMemStoreFailClosedAndConfiguration(t *testing.T) {
	s := state.NewMemStore()
	a, app, stable, d := healthFixture(t, s)
	zero := int64(0)
	g, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout"}}})
	if err != nil {
		t.Fatal(err)
	}
	g.Routes[0].Path = "/mutated"
	current, _ := s.GetRouteHealthGate(t.Context(), a.ID, app.ID)
	if current.Routes[0].Path != "/checkout" {
		t.Fatal("caller mutated saved selectors")
	}
	if _, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{}}); !errors.Is(err, state.ErrRouteHealthRevision) {
		t.Fatal("revision fence")
	}
	if _, err := s.GetRouteHealthGate(t.Context(), uuid.NewString(), app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("tenant leak")
	}
	report, err := s.GetRouteHealthReport(t.Context(), a.ID, app.ID, d.ID)
	if err != nil || report.Status != "unknown" || report.Routes[0].Windows[0].Reason != "telemetry_unavailable" {
		t.Fatalf("%+v %v", report, err)
	}
	var decision api.RouteHealthDecision
	if _, _, err := s.AdvanceCanary(t.Context(), d.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteHealthDecision: &decision}); err == nil || decision.Status != "blocked" {
		t.Fatal("unknown telemetry advanced")
	}
	before, _ := s.DeploymentByID(t.Context(), stable.ID)
	if before.TrafficPercent != 99 {
		t.Fatal("blocked advance changed stable traffic")
	}
	if _, _, err := s.RecoverRollout(t.Context(), app.ID, "promote", ""); err == nil {
		t.Fatal("legacy promotion bypassed guard")
	}
	if _, _, err := s.RecoverRollout(t.Context(), app.ID, "abort", ""); err != nil {
		t.Fatal(err)
	}
}
func TestRouteHealthPostgresObservationsAndAtomicAdvance(t *testing.T) {
	for _, scenario := range []string{"healthy", "worker", "regressed", "mixed", "sparse", "method_mismatch", "stage_changed", "config_changed", "ambiguous_stable", "report"} {
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
			g, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: mode, ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout"}, {Method: "GET", Path: "/busy"}}})
			if err != nil {
				t.Fatal(err)
			}
			// Fixture time travel only; production updates always establish a fresh anchor.
			if _, err := pool.Exec(t.Context(), "UPDATE route_health_gates SET updated_at = clock_timestamp() - interval '1 hour' WHERE app_id = $1", app.ID); err != nil {
				t.Fatal(err)
			}
			// Seed one adjacent window on each side of the Go-clock estimate.
			// PostgreSQL computes report windows on its own clock, and the
			// ingestion-lag boundary can be crossed while this fixture is seeded.
			firstWindow := routehealth.Windows(time.Now().UTC().Add(-api.RouteHealthWindow))[0]
			windows := make([]api.RouteHealthWindowEvidence, 0, api.RouteHealthWindows+2)
			for i := 0; i < api.RouteHealthWindows+2; i++ {
				start := firstWindow.Start.Add(time.Duration(i) * api.RouteHealthWindow)
				windows = append(windows, api.RouteHealthWindowEvidence{Start: start, End: start.Add(api.RouteHealthWindow)})
			}
			q := &sqlc.Queries{}
			for i, w := range windows {
				for _, dep := range []state.Deployment{stable, d} {
					for _, route := range g.Routes {
						method := route.Method
						errorsCount := int32(0)
						total := int32(100)
						if dep.ID == d.ID && route.Path == "/checkout" {
							if scenario == "regressed" || scenario == "report" || scenario == "mixed" && i%2 == 1 {
								errorsCount = 10
							}
							if scenario == "sparse" {
								total = 19
							}
							if scenario == "method_mismatch" {
								method = "GET"
							}
						}
						for _, entry := range []struct{ status, count int32 }{{200, total - errorsCount}, {500, errorsCount}} {
							if entry.count == 0 {
								continue
							}
							params := sqlc.InsertRequestTelemetryParams{AccountID: pgtype.UUID{Bytes: uuid.MustParse(a.ID), Valid: true}, AppID: pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}, DeploymentID: pgtype.UUID{Bytes: uuid.MustParse(dep.ID), Valid: true}, Route: method + " " + route.Path, Method: method, Status: entry.status, LatencyMs: 10, ReceivedAt: state.NewPgtypeTime(w.Start.Add(10 * time.Second)), Count: entry.count, UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", GuestRuntime: "__unknown__", GuestOutcome: "missing", FlagEvidenceJson: "[]"}
							if err := q.InsertRequestTelemetry(t.Context(), pool, params); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
			}
			if scenario == "stage_changed" {
				if _, err := pool.Exec(t.Context(), "UPDATE deployments SET canary_step_started_at = clock_timestamp() WHERE id = $1", d.ID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "config_changed" {
				g.Routes = append(g.Routes, api.RouteHealthRoute{Method: "GET", Path: "/new"})
				if _, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: mode, ExpectedRevision: &g.Revision, Routes: g.Routes}); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "ambiguous_stable" {
				extra, err := s.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:extra", TrafficPercent: 1, CanaryPreset: "balanced", CanaryTotalSteps: 4, RolloutState: "pending"})
				if err != nil {
					t.Fatal(err)
				}
				if err := s.MarkDeploymentLive(t.Context(), extra.ID); err != nil {
					t.Fatal(err)
				}
			}
			r, err := s.GetRouteHealthReport(t.Context(), a.ID, app.ID, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "unknown"
			if scenario == "healthy" || scenario == "worker" {
				want = "healthy"
			}
			if scenario == "regressed" || scenario == "report" {
				want = "regressed"
			}
			if r.Status != want {
				t.Fatalf("%s: %+v", scenario, r)
			}
			if scenario != "ambiguous_stable" && r.StableDeploymentID != stable.ID {
				t.Fatal("comparison not bound to exact predecessor")
			}
			if r.Routes[1].Windows[0].Candidate.Requests != 100 && scenario != "ambiguous_stable" {
				t.Fatal("collapsed counts were not weighted")
			}
			if _, err := s.GetRouteHealthReport(t.Context(), uuid.NewString(), app.ID, d.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("tenant leak")
			}
			if scenario == "healthy" {
				// A concurrent intent update must be observed after the parent lock wait.
				tx, err := pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
				if _, err := tx.Exec(t.Context(), "SELECT id FROM apps WHERE id = $1 FOR UPDATE", app.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(t.Context(), "UPDATE route_health_gates SET updated_at = clock_timestamp() WHERE app_id = $1", app.ID); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					_, _, err := s.AdvanceCanary(t.Context(), d.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10})
					done <- err
				}()
				select {
				case err := <-done:
					t.Fatalf("advance crossed uncommitted intent: %v", err)
				case <-time.After(100 * time.Millisecond):
				}
				if err := tx.Commit(t.Context()); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					var blocked *state.RouteHealthBlockedError
					if !errors.As(err, &blocked) {
						t.Fatalf("advance reused old intent evidence: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("advance stalled after intent commit")
				}
				// Restore only the test fixture's old anchor for the healthy transition below.
				if _, err := pool.Exec(t.Context(), "UPDATE route_health_gates SET updated_at = clock_timestamp() - interval '1 hour' WHERE app_id = $1", app.ID); err != nil {
					t.Fatal(err)
				}
			}
			var decision api.RouteHealthDecision
			params := state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteHealthDecision: &decision, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "route-health-test"}}
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
				if err != nil || auditID == 0 || updated.TrafficPercent != 10 || decision.Status == "blocked" {
					t.Fatalf("advance %+v %+v %v", updated, decision, err)
				}
				audits, err := s.ListDeploymentAudit(t.Context(), d.ID, 10)
				if err != nil || len(audits) != 1 || !strings.Contains(string(audits[0].Data), "route_health") || strings.Contains(string(audits[0].Data), "/checkout") {
					t.Fatal("audit missing or route details leaked")
				}
				next, err := s.GetRouteHealthReport(t.Context(), a.ID, app.ID, d.ID)
				if err != nil || next.Status != "unknown" {
					t.Fatal("new stage reused old evidence")
				}
			} else {
				var blocked *state.RouteHealthBlockedError
				if !errors.As(err, &blocked) || decision.Status != "blocked" {
					t.Fatalf("failed open: %+v %v", decision, err)
				}
				current, _ := s.DeploymentByID(t.Context(), d.ID)
				if current.CanaryStep != 0 || current.TrafficPercent != 1 {
					t.Fatal("blocked transition wrote traffic")
				}
				audits, err := s.ListDeploymentAudit(t.Context(), d.ID, 10)
				if err != nil || len(audits) != 0 {
					t.Fatal("blocked transition wrote audit")
				}
			}
		})
	}
}
