package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 847
// A probed route with no organic traffic reaches a verdict from probe rows,
// rounds are claimed once per minute, and old rows are pruned.
func TestRouteProbePostgresSyntheticEvidence(t *testing.T) {
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
			route := api.RouteHealthRoute{Method: "GET", Path: "/reports/{id}", Probe: &api.RouteHealthProbe{Path: "/reports/7"}}
			if _, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{route}}); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), "UPDATE route_health_gates SET updated_at = clock_timestamp() - interval '1 hour' WHERE app_id = $1", app.ID); err != nil {
				t.Fatal(err)
			}
			targets, err := s.ListRouteProbeTargets(t.Context())
			if err != nil || len(targets) != 1 || targets[0].AppID != app.ID || targets[0].CandidateID != d.ID {
				t.Fatalf("probe targets = %+v, %v", targets, err)
			}
			minute := time.Now().UTC().Truncate(time.Minute)
			if ok, err := s.ClaimRouteProbeRound(t.Context(), app.ID, minute); err != nil || !ok {
				t.Fatalf("first claim = %t, %v", ok, err)
			}
			if ok, err := s.ClaimRouteProbeRound(t.Context(), app.ID, minute); err != nil || ok {
				t.Fatalf("second claim = %t, %v", ok, err)
			}
			observations := []state.RouteProbeObservation{}
			for m := 1; m <= 12; m++ {
				at := minute.Add(-time.Duration(m) * time.Minute)
				errorsCount := int64(0)
				if scenario == "regressed" {
					errorsCount = 5
				}
				observations = append(observations,
					state.RouteProbeObservation{DeploymentID: d.ID, Method: "GET", Path: "/reports/{id}", WindowStart: at, Requests: 10, ServerErrors: errorsCount},
					state.RouteProbeObservation{DeploymentID: stable.ID, Method: "GET", Path: "/reports/{id}", WindowStart: at, Requests: 10},
				)
			}
			if err := s.RecordRouteProbeObservations(t.Context(), a.ID, app.ID, observations); err != nil {
				t.Fatal(err)
			}
			r, err := s.GetRouteHealthReport(t.Context(), a.ID, app.ID, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			f := r.Routes[0]
			if f.Status != scenario || f.EvidenceWindow != "synthetic" || len(f.SyntheticWindows) != api.RouteHealthWindows {
				t.Fatalf("synthetic %s finding = %+v", scenario, f)
			}
			for _, w := range f.SyntheticWindows {
				if w.Candidate.Requests < api.RouteHealthMinRequests || w.Stable.Requests < api.RouteHealthMinRequests {
					t.Fatalf("synthetic window lacks probes: %+v", w)
				}
			}
			if err := s.PruneRouteProbeData(t.Context(), minute.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			pruned, err := s.GetRouteHealthReport(t.Context(), a.ID, app.ID, d.ID)
			if err != nil || pruned.Routes[0].EvidenceWindow != "" {
				t.Fatalf("pruned probes still produced evidence: %+v %v", pruned.Routes[0], err)
			}
		})
	}
}
