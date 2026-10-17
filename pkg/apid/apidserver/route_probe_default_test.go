package apidserver_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/routeprobe"
	"github.com/onebox-faas/faas/pkg/state"
)

// defaultProbeHarness records probe challenges and answers probes that carry
// the issued token.
type defaultProbeHarness struct {
	apidserver.NoopNotifier
	mu       sync.Mutex
	tokens   map[string]string
	outcomes map[string]routeprobe.Outcome
	paths    map[string]int
}

func (h *defaultProbeHarness) Notify(_ context.Context, channel, payload string) error {
	if channel != db.NotifyRouteProbeChallenge {
		return nil
	}
	var p struct {
		DeploymentID string `json:"deployment_id"`
		Token        string `json:"token"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tokens[p.DeploymentID] = p.Token
	return nil
}

func (h *defaultProbeHarness) Configured() bool { return true }

func (h *defaultProbeHarness) Probe(_ context.Context, _ string, deploymentID, token, method, path string) (routeprobe.Outcome, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.paths[method+" "+path]++
	if token == "" || token != h.tokens[deploymentID] {
		return routeprobe.Unattributed, nil
	}
	return h.outcomes[deploymentID], nil
}

// adr: 1446
// A safe-policy canary with no route health probes of its own gets the
// default probe of "/" on the candidate and its predecessor, once per minute
// and with the circuit breaker's request floor per deployment (ADR-1321),
// unless the app opted into immediate releases or the operator turned the
// safe default off.
func TestRouteProbeWorkerProbesSafeReleaseCanaryDefaultPath(t *testing.T) {
	old := *apidserver.RouteProbeChallengeDelay
	*apidserver.RouteProbeChallengeDelay = 0
	t.Cleanup(func() { *apidserver.RouteProbeChallengeDelay = old })
	n := api.DefaultRouteProbeRequestsPerRound
	for _, tc := range []struct {
		name       string
		policy     string
		killSwitch bool
		wantProbes int
	}{
		{name: "safe policy canary", wantProbes: 2 * n},
		{name: "immediate policy", policy: api.ReleasePolicyImmediate},
		{name: "operator kill switch", killSwitch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.killSwitch {
				t.Setenv(release.DefaultEnabledEnv, "false")
			}
			e := apidserver.Setup(t, api.PlanPro)
			ctx := context.Background()
			store := *e.Store()
			app, stable, canary := apidserver.SeedCanaryFixture(t, e, "default-probe")
			if tc.policy != "" {
				manifest := app.Manifest
				manifest.ReleasePolicy = tc.policy
				if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
					t.Fatal(err)
				}
			}
			h := &defaultProbeHarness{tokens: map[string]string{}, paths: map[string]int{},
				outcomes: map[string]routeprobe.Outcome{canary.ID: routeprobe.ServerError, stable.ID: routeprobe.Success}}
			s := *e.S()
			*s.Notif() = h
			s.WithRouteProbes(h)

			// Two drains in the same minute make one round.
			for range 2 {
				if err := s.DrainRouteProbes(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if got := h.paths["GET "+release.DefaultProbePath]; got != tc.wantProbes || len(h.paths) > 1 {
				t.Fatalf("probes = %v, want %d of GET %s", h.paths, tc.wantProbes, release.DefaultProbePath)
			}
			if tc.wantProbes == 0 {
				return
			}
			got := map[string]state.RouteProbeObservation{}
			for _, o := range store.RouteProbeObservationsForTest(app.ID) {
				got[o.DeploymentID] = o
			}
			want := int64(n)
			if c := got[canary.ID]; c.Requests != want || c.ServerErrors != want || c.Path != release.DefaultProbePath {
				t.Fatalf("candidate observation = %+v", c)
			}
			if st := got[stable.ID]; st.Requests != want || st.ServerErrors != 0 || st.Path != release.DefaultProbePath {
				t.Fatalf("stable observation = %+v", st)
			}
		})
	}
}
