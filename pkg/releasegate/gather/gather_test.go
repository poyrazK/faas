package gather_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/releasegate"
	"github.com/onebox-faas/faas/pkg/releasegate/gather"
	"github.com/onebox-faas/faas/pkg/state"
)

// latestValueEvidence is the Postgres evidence shape: app_custom_metrics
// keeps only each metric's latest value, so a window holds at most one.
type latestValueEvidence struct{}

func (latestValueEvidence) ReleaseGateSLO(context.Context, string, string) (state.ReleaseGateSLOTarget, error) {
	return state.ReleaseGateSLOTarget{}, state.ErrNotFound
}

func (latestValueEvidence) ReleaseGateDeploymentSLI(context.Context, string, string, time.Time, time.Time, int) (state.ReleaseGateSLISample, error) {
	return state.ReleaseGateSLISample{}, nil
}

func (latestValueEvidence) ReleaseGateNewErrorGroups(context.Context, string, string, int) ([]state.ReleaseGateErrorGroup, error) {
	return nil, nil
}

func (latestValueEvidence) ReleaseGateCustomMetricWindow(context.Context, string, string, time.Time, time.Time) (int64, float64, error) {
	return 1, 1, nil
}

func (latestValueEvidence) ReleaseGateDeploymentAI(context.Context, string, string, time.Time, time.Time) (state.ReleaseGateAIWindow, error) {
	return state.ReleaseGateAIWindow{}, nil
}

type history struct {
	samples int64
	average float64
	asked   string
}

func (h *history) CustomMetricWindow(_ context.Context, _, name string, _, _ time.Time) (int64, float64, error) {
	h.asked = name
	return h.samples, h.average, nil
}

// adr: 20261019071734 amendment 1 — a custom_metric gate is judged on the
// metric's history over the window; the latest stored value alone never
// reaches min_samples.
func TestCustomMetricGateReadsTheMetricHistory(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	now := time.Now()
	if _, err := store.CreateReleaseGate(ctx, state.ReleaseGate{AppID: "app-1", AccountID: "acct-1", Name: "orders", Kind: releasegate.KindCustomMetric,
		MetricName: "orders_per_minute", Comparison: "below", Threshold: 5, MinSamples: 20}, 10, now); err != nil {
		t.Fatal(err)
	}
	window := gather.Window{Since: now.Add(-5 * time.Minute), Until: now}
	req := gather.Request{AppID: "app-1", ReleaseID: "dep-2", BaselineID: "dep-1", ReleaseWindow: window, BaselineWindow: window,
		Evidence: latestValueEvidence{}}

	verdict, err := gather.Verdict(ctx, store, req)
	if err != nil || verdict.Failed != nil {
		t.Fatalf("without history = %+v, %v; want the gate not judged on one stored value", verdict, err)
	}

	h := &history{samples: 25, average: 2}
	req.CustomMetrics = h
	verdict, err = gather.Verdict(ctx, store, req)
	if err != nil || verdict.Failed == nil || verdict.Failed.Name != "orders" {
		t.Fatalf("with history = %+v, %v; want the orders gate failed on 25 samples averaging 2", verdict, err)
	}
	if h.asked != "orders_per_minute" {
		t.Fatalf("history asked for %q, want orders_per_minute", h.asked)
	}
}

// windowHistory answers each window, keyed by its end, from the history.
type windowHistory map[time.Time]releasegate.MetricWindow

func (h windowHistory) CustomMetricWindow(_ context.Context, _, _ string, _, until time.Time) (int64, float64, error) {
	w := h[until]
	return w.Samples, w.Average, nil
}

// adr: 20261022082305 — a metric_change gate reads both its windows from the
// metric's history, like a custom_metric gate: the stored latest value never
// fills the window before the rollout.
func TestMetricChangeGateReadsBothWindowsFromTheMetricHistory(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	now := time.Now()
	if _, err := store.CreateReleaseGate(ctx, state.ReleaseGate{AppID: "app-1", AccountID: "acct-1", Name: "orders-drop", Kind: releasegate.KindMetricChange,
		MetricName: "orders_per_minute", Comparison: "below", MaxChangePercent: 30, MinSamples: 20}, 10, now); err != nil {
		t.Fatal(err)
	}
	release := gather.Window{Since: now.Add(-5 * time.Minute), Until: now}
	prior := gather.Window{Since: now.Add(-10 * time.Minute), Until: release.Since}
	req := gather.Request{AppID: "app-1", ReleaseID: "dep-2", BaselineID: "dep-1", ReleaseWindow: release, BaselineWindow: release,
		PriorWindow: prior, Canary: true, Evidence: latestValueEvidence{}}

	verdict, err := gather.Verdict(ctx, store, req)
	if err != nil || verdict.Failed != nil {
		t.Fatalf("without history = %+v, %v; want the gate not judged on one stored value", verdict, err)
	}

	req.CustomMetrics = windowHistory{
		prior.Until:   {Samples: 25, Average: 50},
		release.Until: {Samples: 25, Average: 20},
	}
	verdict, err = gather.Verdict(ctx, store, req)
	if err != nil || verdict.Failed == nil || verdict.Failed.Name != "orders-drop" || verdict.Failed.Observed != -60 {
		t.Fatalf("with history = %+v, %v; want orders-drop failed on a 60%% drop", verdict, err)
	}
}
