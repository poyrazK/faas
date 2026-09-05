// simulator_test.go — SAFE-RELEASES-OBS PR-E (issue #976 /
// ADR-122). Pins the simulator's projection math and the closed-set
// error surfaces. Uses WithAggregate to skip the source seam —
// the simulator's job is the projection; the Store seam is
// exercised separately in pkg/state.
package canary

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"
)

// fakeSource is a hand-rolled SimulatorSource that returns canned
// values. The simulator's source-driven path uses two goroutines +
// channels; the fake doesn't need to model that — it just returns
// the canned values directly.
type fakeSource struct {
	errRate float64
	p95Ms   float64
	err     error
}

func (f *fakeSource) RecentErrorRate(_ context.Context, _ string, _ time.Time) (float64, error) {
	return f.errRate, f.err
}
func (f *fakeSource) RecentP95LatencyMs(_ context.Context, _ string, _ time.Time) (float64, error) {
	return f.p95Ms, f.err
}

// makeSamples builds N InvocationSample rows spaced `step` apart
// starting at base. Defaults: 10% failed (for the error-rate math to
// bite on a per-stage projection).
func makeSamples(n int, base time.Time, step time.Duration) []InvocationSample {
	out := make([]InvocationSample, n)
	for i := 0; i < n; i++ {
		created := base.Add(time.Duration(i) * step)
		out[i] = InvocationSample{
			CreatedAt:   created,
			CompletedAt: created.Add(50 * time.Millisecond),
			Failed:      i%10 == 0,
		}
	}
	return out
}

// TestSimulateCanary_HappyPath pins the basic projection: with
// 1000 invocations and 10% error rate, the balanced preset's
// per-step probabilities drop monotonically as the stage
// traffic-share grows. A regression here means the cumulative
// ProjectedSuccess formula broke.
func TestSimulateCanary_HappyPath(t *testing.T) {
	src := &fakeSource{errRate: 0.10, p95Ms: 50}
	base := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	samples := makeSamples(1000, base, time.Second)

	report, err := SimulateCanary(context.Background(), src, "my-app", "balanced", samples)
	if err != nil {
		t.Fatalf("SimulateCanary: %v", err)
	}
	if report.Preset != "balanced" {
		t.Errorf("preset = %q; want balanced", report.Preset)
	}
	if report.Stages != 4 {
		t.Errorf("stages = %d; want 4 (balanced preset)", report.Stages)
	}
	if report.ObservedTraffic != 1000 {
		t.Errorf("observed_traffic = %d; want 1000", report.ObservedTraffic)
	}
	if report.ObservedError != 0.10 {
		t.Errorf("observed_error_rate = %v; want 0.10", report.ObservedError)
	}
	if report.ProjectedSuccess <= 0 || report.ProjectedSuccess >= 1 {
		t.Errorf("projected_success_p = %v; want in (0,1)", report.ProjectedSuccess)
	}
	if report.Note != "" {
		t.Errorf("note = %q; want empty", report.Note)
	}
	// Cumulative ProjectedSuccess = product of per-step success_p.
	prod := 1.0
	for _, s := range report.PerStep {
		prod *= s.SuccessP
	}
	if math.Abs(prod-report.ProjectedSuccess) > 1e-9 {
		t.Errorf("cumulative %v != product %v", report.ProjectedSuccess, prod)
	}
}

// TestSimulateCanary_InsufficientTraffic pins the empty-sample
// fallback: < 5 invocations → Note + neutral 0.5 projection.
func TestSimulateCanary_InsufficientTraffic(t *testing.T) {
	src := &fakeSource{errRate: 0.05, p95Ms: 100}
	base := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	samples := makeSamples(3, base, time.Second) // < 5

	report, err := SimulateCanary(context.Background(), src, "my-app", "balanced", samples)
	if err != nil {
		t.Fatalf("SimulateCanary: %v", err)
	}
	if !strings.Contains(report.Note, "insufficient traffic sample") {
		t.Errorf("note = %q; want 'insufficient traffic sample'", report.Note)
	}
	for i, s := range report.PerStep {
		if s.SuccessP != 0.5 {
			t.Errorf("step %d success_p = %v; want 0.5 (neutral)", i, s.SuccessP)
		}
	}
}

// TestSimulateCanary_UnknownPreset pins the closed-set admission
// gate — a non-AllowedCanaryPresets name surfaces
// ErrSimulatorUnknownPreset.
func TestSimulateCanary_UnknownPreset(t *testing.T) {
	src := &fakeSource{}
	_, err := SimulateCanary(context.Background(), src, "my-app", "bogus-preset", nil)
	if err == nil {
		t.Fatal("expected ErrSimulatorUnknownPreset; got nil")
	}
	if !strings.Contains(err.Error(), "bogus-preset") {
		t.Errorf("error %q should mention the preset name", err)
	}
}

// TestSimulateCanary_EmptyStages pins the "none" preset's
// zero-stages path: preset='none' resolves to LookupPreset with
// len(Stages)==0 → ErrSimulatorEmptyStages. The CLI uses this to
// fast-path the no-canary case.
func TestSimulateCanary_EmptyStages(t *testing.T) {
	src := &fakeSource{}
	_, err := SimulateCanary(context.Background(), src, "my-app", "none", nil)
	if err == nil {
		t.Fatal("expected ErrSimulatorEmptyStages; got nil")
	}
}

// TestSimulateCanary_NilSourceWithOverride pins WithAggregate: when
// override is set, source may be nil. This is the test seam.
func TestSimulateCanary_NilSourceWithOverride(t *testing.T) {
	base := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	samples := makeSamples(1000, base, time.Second)

	report, err := SimulateCanary(context.Background(), nil, "my-app", "balanced", samples,
		WithAggregate(0.05, 25))
	if err != nil {
		t.Fatalf("SimulateCanary: %v", err)
	}
	if report.ObservedError != 0.05 {
		t.Errorf("observed_error_rate = %v; want 0.05", report.ObservedError)
	}
	if report.ObservedP95Ms != 25 {
		t.Errorf("observed_p95_latency_ms = %v; want 25", report.ObservedP95Ms)
	}
}

func TestSimulateCanary_ObservedTrafficOverride(t *testing.T) {
	report, err := SimulateCanary(context.Background(), nil, "my-app", "balanced", nil,
		WithAggregate(0.05, 25), WithObservedTraffic(1000))
	if err != nil {
		t.Fatalf("SimulateCanary: %v", err)
	}
	if report.ObservedTraffic != 1000 {
		t.Fatalf("observed_traffic = %d, want 1000", report.ObservedTraffic)
	}
	if report.Note != "" {
		t.Fatalf("unexpected low-traffic note: %q", report.Note)
	}
	if report.ProjectedSuccess >= 1 {
		t.Fatalf("projected_success_p = %v, want less than 1", report.ProjectedSuccess)
	}
}

// TestSimulateCanary_NilSourceWithoutOverride pins the no-source +
// no-override path: ErrSimulatorNoSource. Defends against a future
// refactor that silently lets the simulator run with empty data.
func TestSimulateCanary_NilSourceWithoutOverride(t *testing.T) {
	_, err := SimulateCanary(context.Background(), nil, "my-app", "balanced", nil)
	if err == nil {
		t.Fatal("expected ErrSimulatorNoSource; got nil")
	}
}

// TestSimulateStep_EdgeCases pins simulateStep's guard branches:
// percent=0 / duration=0 / negative-projection all return 1.0
// (impossible to fail when there's nothing to fail).
func TestSimulateStep_EdgeCases(t *testing.T) {
	cases := []struct {
		name             string
		traffic, errRate float64
		percent          int
		duration         time.Duration
		want             float64
	}{
		{"percent_zero", 1.0, 0.5, 0, time.Minute, 1.0},
		{"duration_zero", 1.0, 0.5, 50, 0, 1.0},
		{"negative_duration", 1.0, 0.5, 50, -time.Minute, 1.0},
		{"zero_error_rate", 1.0, 0.0, 50, time.Minute, 1.0},
		{"err_rate_clamped", 1.0, 1.5, 50, time.Minute, 0.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := simulateStep(tc.traffic, tc.errRate, tc.percent, tc.duration)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("simulateStep = %v; want %v", got, tc.want)
			}
		})
	}
}

// TestFormatTable_HeaderAndRows pins the CLI's default output shape:
// a 4-column header + one row per step + an overall line. Drift here
// means the CLI's grep-friendly output broke.
func TestFormatTable_HeaderAndRows(t *testing.T) {
	r := SimReport{
		Preset:           "balanced",
		Stages:           4,
		ProjectedSuccess: 0.85,
		PerStep: []SimStepEntry{
			{Step: 0, Percent: 1, Duration: "30s", SuccessP: 0.99},
			{Step: 1, Percent: 10, Duration: "2m0s", SuccessP: 0.95},
			{Step: 2, Percent: 50, Duration: "5m0s", SuccessP: 0.92},
			{Step: 3, Percent: 100, Duration: "0s", SuccessP: 1.0},
		},
	}
	tbl := r.FormatTable()
	wantLines := []string{
		"step  percent  duration  success_p",
		"0     1        30s       0.9900",
		"3     100      0s        1.0000",
		"overall: projected_success_p=0.8500",
	}
	for _, w := range wantLines {
		if !strings.Contains(tbl, w) {
			t.Errorf("table missing %q\ntable:\n%s", w, tbl)
		}
	}
}

// TestSampleFromInvocation_FailedStates pins the adapter: 'failed'
// and 'dead_letter' map to Failed=true; everything else maps to
// Failed=false. Drift here means the error-rate computation in
// RecentErrorRate produces different numbers for the simulator vs
// the Store.
func TestSampleFromInvocation_FailedStates(t *testing.T) {
	now := time.Now()
	cases := []struct {
		state    string
		wantFail bool
	}{
		{"failed", true},
		{"dead_letter", true},
		{"completed", false},
		{"cancelled", false},
		{"pending", false},
		{"dispatching", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			s := SampleFromInvocation(now, now, tc.state)
			if s.Failed != tc.wantFail {
				t.Errorf("state=%q: Failed=%v; want %v", tc.state, s.Failed, tc.wantFail)
			}
		})
	}
}
