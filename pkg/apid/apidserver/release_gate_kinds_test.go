package apidserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/releasegate/gather"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261022082305 — synthetic and metric_change gates round-trip, and
// the probe worker sends a synthetic gate's check to the canary and its
// stable revision so the gate can tell them apart.

// statusProbeHarness answers probes with a status code per deployment.
type statusProbeHarness struct {
	defaultProbeHarness
	statuses map[string]int
	calls    map[string]int
}

func (h *statusProbeHarness) ProbeStatus(_ context.Context, _ string, deploymentID, token, method, path string) (int, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls[method+" "+path+" "+deploymentID]++
	if token == "" || token != h.tokens[deploymentID] {
		return 0, false, nil
	}
	return h.statuses[deploymentID], true, nil
}

func TestSyntheticGateProbesTheCanaryAndItsStableRevision(t *testing.T) {
	old := *apidserver.RouteProbeChallengeDelay
	*apidserver.RouteProbeChallengeDelay = 0
	t.Cleanup(func() { *apidserver.RouteProbeChallengeDelay = old })
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	store := *e.Store()
	app, stable, canary := apidserver.SeedCanaryFixture(t, e, "synth-gate")
	// Without the safe default's own probe, the round holds only the gate's.
	manifest := app.Manifest
	manifest.ReleasePolicy = api.ReleasePolicyImmediate
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSyntheticCheck(ctx, state.SyntheticCheck{AccountID: app.AccountID, AppID: app.ID, Name: "checkout",
		Method: http.MethodGet, Path: "/checkout", ExpectedStatus: 200, TimeoutMS: 5000, IntervalSeconds: 300, Enabled: true}, 10); err != nil {
		t.Fatal(err)
	}

	rec := e.Do(t, http.MethodPost, "/v1/apps/synth-gate/release-gates", api.CreateReleaseGateRequest{Name: "missing", Kind: "synthetic", Check: "nope"}, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no synthetic check named nope") {
		t.Fatalf("unknown check = %d %s", rec.Code, rec.Body)
	}
	var gate api.ReleaseGate
	if rec := guardDo(t, e, http.MethodPost, "/v1/apps/synth-gate/release-gates", api.CreateReleaseGateRequest{Name: "checkout-probe", Kind: "synthetic", Check: "checkout"}, &gate); rec.Code != http.StatusCreated {
		t.Fatalf("create synthetic gate = %d %s", rec.Code, rec.Body)
	}
	if gate.Check != "checkout" || gate.MaxFailurePercent == nil || *gate.MaxFailurePercent != api.ReleaseGateDefaultMaxFailurePercent ||
		gate.MinSamples != api.ReleaseGateDefaultSyntheticMinSamples {
		t.Fatalf("synthetic gate = %+v", gate)
	}
	var change api.ReleaseGate
	if rec := guardDo(t, e, http.MethodPost, "/v1/apps/synth-gate/release-gates", api.CreateReleaseGateRequest{Name: "orders-drop", Kind: "metric_change",
		Metric: "orders_per_minute", Comparison: "below", MaxChangePercent: 30}, &change); rec.Code != http.StatusCreated {
		t.Fatalf("create metric_change gate = %d %s", rec.Code, rec.Body)
	}
	if change.Metric != "orders_per_minute" || change.Comparison != "below" || change.MaxChangePercent != 30 || change.Threshold != nil {
		t.Fatalf("metric_change gate = %+v", change)
	}

	h := &statusProbeHarness{defaultProbeHarness: defaultProbeHarness{tokens: map[string]string{}, paths: map[string]int{}},
		statuses: map[string]int{canary.ID: http.StatusNotFound, stable.ID: http.StatusOK}, calls: map[string]int{}}
	s := *e.S()
	*s.Notif() = h
	s.WithRouteProbes(h)
	start := time.Now().UTC()
	for range 3 {
		// One claimed round per minute; the drains after it are no-ops.
		if err := s.DrainRouteProbes(ctx); err != nil {
			t.Fatal(err)
		}
	}
	perRound := api.ReleaseGateSyntheticProbesPerRound
	if got := h.calls["GET /checkout "+canary.ID]; got != perRound || h.calls["GET /checkout "+stable.ID] != perRound || len(h.paths) != 0 {
		t.Fatalf("probes = %v (route probes %v), want %d per revision", h.calls, h.paths, perRound)
	}
	minute := start.Truncate(time.Minute)
	if requests, failures, err := store.ReleaseGateProbeTotals(ctx, gateID(t, store, app.ID, "checkout-probe"), canary.ID, minute, minute.Add(2*time.Minute)); err != nil ||
		requests != int64(perRound) || failures != int64(perRound) {
		t.Fatalf("canary totals = %d/%d, %v", requests, failures, err)
	}

	// Three more rounds' worth of evidence meets min_samples; the canary
	// fails its check while the stable revision passes, so the gate fails.
	id := gateID(t, store, app.ID, "checkout-probe")
	for i := 1; i <= 3; i++ {
		at := minute.Add(time.Duration(-i) * time.Minute)
		if err := store.RecordReleaseGateProbeResults(ctx, []state.ReleaseGateProbeResult{
			{GateID: id, AppID: app.ID, DeploymentID: canary.ID, WindowStart: at, Requests: 2, Failures: 2},
			{GateID: id, AppID: app.ID, DeploymentID: stable.ID, WindowStart: at, Requests: 2},
		}); err != nil {
			t.Fatal(err)
		}
	}
	window := gather.Window{Since: minute.Add(-5 * time.Minute), Until: start.Add(time.Minute)}
	verdict, err := gather.Verdict(ctx, store, gather.Request{AppID: app.ID, ReleaseID: canary.ID, BaselineID: stable.ID,
		ReleaseWindow: window, BaselineWindow: window, Canary: true})
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Failed == nil || verdict.Failed.Name != "checkout-probe" || verdict.Failed.Kind != "synthetic" ||
		!strings.Contains(verdict.Summary(), "checkout failed 8 of 8 probes") {
		t.Fatalf("verdict = %+v", verdict)
	}
	// The post-release watch does not judge synthetic gates.
	verdict, err = gather.Verdict(ctx, store, gather.Request{AppID: app.ID, ReleaseID: canary.ID, BaselineID: stable.ID,
		ReleaseWindow: window, BaselineWindow: window})
	if err != nil || len(verdict.Results) != 0 {
		t.Fatalf("post-release verdict = %+v, %v", verdict, err)
	}
}

// gateID reads a gate's ID by name.
func gateID(t *testing.T, store *state.MemStore, appID, name string) string {
	t.Helper()
	gates, err := store.ListReleaseGates(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range gates {
		if g.Name == name {
			return g.ID
		}
	}
	t.Fatalf("no gate %s", name)
	return ""
}
