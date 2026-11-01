package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// testReleaseGateProbes pins ADR-20261022082305 on both stores: synthetic and
// metric_change gates round-trip, a canary's synthetic gates are listed for
// probing with their check, probe counts add up per gate, revision and
// minute, and deleting the check deletes its gates.
func testReleaseGateProbes(t *testing.T, fx *Fixture) {
	t.Helper()
	gates, ok := fx.Store.(state.ReleaseGateStore)
	if !ok {
		t.Fatalf("%T does not implement state.ReleaseGateStore", fx.Store)
	}
	probes, ok := fx.Store.(state.ReleaseGateProbeStore)
	if !ok {
		t.Fatalf("%T does not implement state.ReleaseGateProbeStore", fx.Store)
	}
	checks, ok := fx.Store.(state.SyntheticCheckStore)
	if !ok {
		t.Fatalf("%T does not implement state.SyntheticCheckStore", fx.Store)
	}
	acct, app := fx.Account.ID, fx.App.ID
	now := time.Now().UTC().Truncate(time.Second)
	check, err := checks.CreateSyntheticCheck(fx.Ctx, state.SyntheticCheck{AccountID: acct, AppID: app, Name: "checkout", Method: "GET",
		Path: "/checkout", ExpectedStatus: 200, TimeoutMS: 5000, IntervalSeconds: 300, Enabled: true}, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []state.ReleaseGate{
		{AccountID: acct, AppID: app, Name: "checkout-probe", Kind: state.ReleaseGateSynthetic, SyntheticCheckID: check.ID,
			MaxFailurePercent: 0, MinSamples: 6, CreatedBy: "user:conformance"},
		{AccountID: acct, AppID: app, Name: "orders-drop", Kind: state.ReleaseGateMetricChange, MetricName: "orders_per_minute",
			Comparison: "below", MaxChangePercent: 30, MinSamples: 5, CreatedBy: "user:conformance"},
	} {
		if _, err := gates.CreateReleaseGate(fx.Ctx, g, 10, now); err != nil {
			t.Fatalf("create %s: %v", g.Name, err)
		}
	}
	listed, err := gates.ListReleaseGates(fx.Ctx, app)
	if err != nil || len(listed) != 2 {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
	synthetic, change := listed[0], listed[1]
	if synthetic.Kind != state.ReleaseGateSynthetic || synthetic.SyntheticCheckID != check.ID || synthetic.MaxFailurePercent != 0 || synthetic.MetricName != "" {
		t.Fatalf("synthetic gate = %+v", synthetic)
	}
	if change.Kind != state.ReleaseGateMetricChange || change.MaxChangePercent != 30 || change.Comparison != "below" || change.Threshold != 0 || change.SyntheticCheckID != "" {
		t.Fatalf("metric_change gate = %+v", change)
	}

	// Nothing to probe until a canary rolls out beside the serving release.
	if targets, err := probes.ListSyntheticGateProbeTargets(fx.Ctx, 10); err != nil || len(targets) != 0 {
		t.Fatalf("targets without a canary = %+v, %v", targets, err)
	}
	stepStarted := now
	candidate, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:probe-" + uuid.NewString(), Status: state.DeployPending, CanaryPreset: "balanced",
		CanaryTotalSteps: 4, TrafficPercent: 10, CanaryStepStartedAt: &stepStarted})
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.MarkDeploymentLive(fx.Ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	targets, err := probes.ListSyntheticGateProbeTargets(fx.Ctx, 10)
	if err != nil || len(targets) != 1 {
		t.Fatalf("targets = %+v, %v", targets, err)
	}
	if tg := targets[0]; tg.AppID != app || tg.CandidateID != candidate.ID || tg.StableID != fx.Deployment.ID || tg.GateID != synthetic.ID ||
		tg.Method != "GET" || tg.Path != "/checkout" || tg.ExpectedStatus != 200 || tg.TimeoutMS != 5000 || tg.Slug != fx.App.Slug {
		t.Fatalf("target = %+v", tg)
	}

	minute := now.Truncate(time.Minute)
	round := []state.ReleaseGateProbeResult{
		{GateID: synthetic.ID, AppID: app, DeploymentID: candidate.ID, WindowStart: minute, Requests: 2, Failures: 1},
		{GateID: synthetic.ID, AppID: app, DeploymentID: fx.Deployment.ID, WindowStart: minute, Requests: 2},
	}
	for range 2 {
		if err := probes.RecordReleaseGateProbeResults(fx.Ctx, round); err != nil {
			t.Fatal(err)
		}
	}
	if err := probes.RecordReleaseGateProbeResults(fx.Ctx, []state.ReleaseGateProbeResult{
		{GateID: synthetic.ID, AppID: app, DeploymentID: candidate.ID, WindowStart: minute, Requests: 1, Failures: 2},
	}); err == nil {
		t.Fatal("more failures than probes was recorded")
	}
	requests, failures, err := probes.ReleaseGateProbeTotals(fx.Ctx, synthetic.ID, candidate.ID, minute, minute.Add(time.Minute))
	if err != nil || requests != 4 || failures != 2 {
		t.Fatalf("candidate totals = %d/%d, %v; want 4 probes, 2 failed", requests, failures, err)
	}
	if requests, failures, err := probes.ReleaseGateProbeTotals(fx.Ctx, synthetic.ID, fx.Deployment.ID, minute, minute.Add(time.Minute)); err != nil || requests != 4 || failures != 0 {
		t.Fatalf("stable totals = %d/%d, %v", requests, failures, err)
	}
	if requests, _, err := probes.ReleaseGateProbeTotals(fx.Ctx, synthetic.ID, candidate.ID, minute.Add(time.Minute), minute.Add(2*time.Minute)); err != nil || requests != 0 {
		t.Fatalf("totals after the minute = %d, %v", requests, err)
	}
	if err := probes.PruneReleaseGateProbeResults(fx.Ctx, minute.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if requests, _, err := probes.ReleaseGateProbeTotals(fx.Ctx, synthetic.ID, candidate.ID, minute, minute.Add(time.Minute)); err != nil || requests != 0 {
		t.Fatalf("totals after pruning = %d, %v", requests, err)
	}

	// A disabled check is not probed; deleting it deletes its gate.
	if _, err := checks.SetSyntheticCheckEnabled(fx.Ctx, app, check.ID, false); err != nil {
		t.Fatal(err)
	}
	if targets, err := probes.ListSyntheticGateProbeTargets(fx.Ctx, 10); err != nil || len(targets) != 0 {
		t.Fatalf("targets with the check disabled = %+v, %v", targets, err)
	}
	if err := checks.DeleteSyntheticCheck(fx.Ctx, app, check.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := gates.DeleteReleaseGate(fx.Ctx, app, "checkout-probe"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("gate after its check was deleted: err = %v, want ErrNotFound", err)
	}
	if listed, err := gates.ListReleaseGates(fx.Ctx, app); err != nil || len(listed) != 1 || listed[0].Name != "orders-drop" {
		t.Fatalf("gates after the check was deleted = %+v, %v", listed, err)
	}
}
