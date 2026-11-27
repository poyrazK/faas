package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// setRolloutState moves a deployment's rollout to state without touching its
// traffic split, the way an aborted canary kept its split in production.
type setRolloutState func(t *testing.T, deploymentID, rolloutState string)

// adr: 847
func TestRouteProbeTargetsSkipEndedRolloutsMemStore(t *testing.T) {
	for _, ended := range []string{"aborted", "complete"} {
		t.Run(ended, func(t *testing.T) {
			s := state.NewMemStore()
			checkRouteProbeTargetsSkipEndedRollout(t, s, ended, func(t *testing.T, id, rolloutState string) {
				t.Helper()
				if err := s.SetDeploymentCanaryState(t.Context(), id, "balanced", 0, 4, time.Now().Add(-time.Hour), rolloutState); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

// adr: 847
// Only "aborted": deployments_app_scope_live_uniq forbids a completed rollout
// beside its live predecessor, so Postgres cannot hold the "complete" case.
func TestRouteProbeTargetsSkipEndedRolloutsPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	checkRouteProbeTargetsSkipEndedRollout(t, state.NewPgStore(pool), "aborted", func(t *testing.T, id, rolloutState string) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), "UPDATE deployments SET rollout_state = $2 WHERE id = $1", id, rolloutState); err != nil {
			t.Fatal(err)
		}
	})
}

// checkRouteProbeTargetsSkipEndedRollout: a canary candidate that still
// carries traffic is a target of all three probe listers (ADR-847 opted-in
// routes, ADR-1446 default path, ADR-20261022082305 synthetic gates) while its
// rollout is pending or rolling out, and of none once the rollout ended.
// Before this rule, aborted canaries that kept their split were probed every
// minute and their instances never went idle (ADR-847 amendment 3).
func checkRouteProbeTargetsSkipEndedRollout(t *testing.T, s state.Store, ended string, set setRolloutState) {
	t.Helper()
	ctx := t.Context()
	account, app, stable, candidate := healthFixture(t, s)
	zero := int64(0)
	route := api.RouteHealthRoute{Method: "GET", Path: "/reports/{id}", Probe: &api.RouteHealthProbe{Path: "/reports/7"}}
	if _, err := s.(state.RouteHealthStore).SetRouteHealthGate(ctx, account.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{route}}); err != nil {
		t.Fatal(err)
	}
	check, err := s.(state.SyntheticCheckStore).CreateSyntheticCheck(ctx, state.SyntheticCheck{AccountID: account.ID, AppID: app.ID, Name: "checkout",
		Method: "GET", Path: "/checkout", ExpectedStatus: 200, TimeoutMS: 5000, IntervalSeconds: 300, Enabled: true}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.(state.ReleaseGateStore).CreateReleaseGate(ctx, state.ReleaseGate{AccountID: account.ID, AppID: app.ID, Name: "checkout-probe",
		Kind: state.ReleaseGateSynthetic, SyntheticCheckID: check.ID, MinSamples: 6, CreatedBy: "user:test"}, 10, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	for _, inFlight := range []string{"pending", "rolling_out"} {
		set(t, candidate.ID, inFlight)
		got := probeTargetCandidates(ctx, t, s)
		for lister, ids := range got {
			if len(ids) != 1 || ids[0] != candidate.ID {
				t.Fatalf("rollout %s: %s targets = %v, want [%s] (stable %s)", inFlight, lister, ids, candidate.ID, stable.ID)
			}
		}
	}

	set(t, candidate.ID, ended)
	for lister, ids := range probeTargetCandidates(ctx, t, s) {
		if len(ids) != 0 {
			t.Fatalf("rollout %s: %s targets = %v, want none", ended, lister, ids)
		}
	}
}

// probeTargetCandidates returns each probe lister's candidate IDs.
func probeTargetCandidates(ctx context.Context, t *testing.T, s state.Store) map[string][]string {
	t.Helper()
	out := map[string][]string{"route": {}, "default": {}, "synthetic": {}}
	routes, err := s.(state.RouteProbeStore).ListRouteProbeTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range routes {
		out["route"] = append(out["route"], tg.CandidateID)
	}
	defaults, err := s.(state.DefaultRouteProbeStore).ListDefaultRouteProbeTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range defaults {
		out["default"] = append(out["default"], tg.CandidateID)
	}
	synthetic, err := s.(state.ReleaseGateProbeStore).ListSyntheticGateProbeTargets(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range synthetic {
		out["synthetic"] = append(out["synthetic"], tg.CandidateID)
	}
	return out
}
