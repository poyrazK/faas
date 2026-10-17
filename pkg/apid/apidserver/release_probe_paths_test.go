package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/routeprobe"
)

// TestReleaseProbePathsRoundTripAndProbe pins ADR-1449: an app sets the
// paths its default rollout probe checks, invalid paths are refused, and a
// probe round splits the request floor across the named paths instead of
// probing "/".
func TestReleaseProbePathsRoundTripAndProbe(t *testing.T) {
	old := *apidserver.RouteProbeChallengeDelay
	*apidserver.RouteProbeChallengeDelay = 0
	t.Cleanup(func() { *apidserver.RouteProbeChallengeDelay = old })
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	store := *e.Store()
	app, stable, canary := apidserver.SeedCanaryFixture(t, e, "probe-paths")

	for _, bad := range [][]string{{"healthz"}, {"/a?b=1"}, {"/x", "/x"}, {"/a", "/b", "/c", "/d"}} {
		rec := e.Do(t, http.MethodPatch, "/v1/apps/"+app.Slug, map[string]any{"release_probe_paths": bad}, nil)
		if rec.Code < 400 || rec.Code >= 500 {
			t.Fatalf("release_probe_paths %q = %d %s, want a 4xx", bad, rec.Code, rec.Body.String())
		}
	}
	paths := []string{"/healthz", "/api/orders"}
	rec := e.Do(t, http.MethodPatch, "/v1/apps/"+app.Slug, map[string]any{"release_probe_paths": paths}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("set release_probe_paths = %d %s", rec.Code, rec.Body.String())
	}
	got := e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug, nil, nil)
	var resp api.AppResponse
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &resp) != nil || len(resp.ReleaseProbePaths) != 2 || resp.ReleaseProbePaths[1] != "/api/orders" {
		t.Fatalf("app response release_probe_paths = %v (%d %s)", resp.ReleaseProbePaths, got.Code, got.Body.String())
	}

	h := &defaultProbeHarness{tokens: map[string]string{}, paths: map[string]int{},
		outcomes: map[string]routeprobe.Outcome{canary.ID: routeprobe.Success, stable.ID: routeprobe.Success}}
	s := *e.S()
	*s.Notif() = h
	s.WithRouteProbes(h)
	if err := s.DrainRouteProbes(ctx); err != nil {
		t.Fatal(err)
	}
	per := (api.DefaultRouteProbeRequestsPerRound + 1) / 2
	if len(h.paths) != 2 || h.paths["GET /healthz"] != 2*per || h.paths["GET /api/orders"] != 2*per {
		t.Fatalf("probes = %v, want %d of each named path across both deployments and none of /", h.paths, 2*per)
	}
	total := map[string]int64{}
	for _, o := range store.RouteProbeObservationsForTest(app.ID) {
		total[o.DeploymentID] += o.Requests
	}
	if total[canary.ID] < api.DefaultRouteProbeRequestsPerRound || total[stable.ID] < api.DefaultRouteProbeRequestsPerRound {
		t.Fatalf("probe totals = %v, want at least the %d-request floor per deployment", total, api.DefaultRouteProbeRequestsPerRound)
	}

	rec = e.Do(t, http.MethodPatch, "/v1/apps/"+app.Slug, map[string]any{"release_probe_paths": []string{}}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear release_probe_paths = %d %s", rec.Code, rec.Body.String())
	}
	if cleared, err := store.AppByID(ctx, app.ID); err != nil || len(cleared.Manifest.ReleaseProbePaths) != 0 {
		t.Fatalf("cleared manifest = %+v, %v", cleared.Manifest.ReleaseProbePaths, err)
	}
}
