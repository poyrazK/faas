package builderd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// This fake implements only the reaper's narrow contracts, not state.Store.
type observedReaperStore struct {
	swept                         int
	sweepErr, claimErr, recordErr error
	claims                        []state.BuildVMCleanupClaim
	recordedCleanupErr            error
}

func (s *observedReaperStore) SweepStuckRunningBuilds(context.Context, time.Time) (int, error) {
	return s.swept, s.sweepErr
}
func (s *observedReaperStore) ClaimBuildVMCleanup(context.Context, int) ([]state.BuildVMCleanupClaim, error) {
	return s.claims, s.claimErr
}
func (s *observedReaperStore) CompleteBuildVMCleanup(_ context.Context, _, _ string, err error) error {
	s.recordedCleanupErr = err
	return s.recordErr
}

func TestReaperMetricsWholePassOutcomes(t *testing.T) {
	failed := errors.New("unavailable")
	for _, tc := range []struct {
		name                          string
		sweepErr, claimErr, recordErr error
		cancelFail                    bool
		wantSwept                     int
		wantLegacyErr, wantOK         bool
	}{
		{"success", nil, nil, nil, false, 1, false, true},
		{"sweep failure", failed, nil, nil, false, 0, true, false},
		{"unexpected not found is not success", state.ErrNotFound, nil, nil, false, 0, true, false},
		{"claim failure after recovered row", nil, failed, nil, false, 1, true, false},
		{"VM stop failure after recovered row", nil, nil, nil, true, 1, false, false},
		{"result record failure after VM stop", nil, nil, failed, false, 1, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewPedanticRegistry()
			m := NewReaperMetrics(reg, time.Minute, 25*time.Minute)
			now := time.Unix(1700000000, 0)
			m.now = func() time.Time { return now }
			s := &observedReaperStore{swept: tc.wantSwept, sweepErr: tc.sweepErr, claimErr: tc.claimErr, recordErr: tc.recordErr, claims: []state.BuildVMCleanupClaim{{BuildID: "a", ClaimToken: "token"}}}
			vm := &retryReaperVM{failFirst: tc.cancelFail}
			m.begin()
			now = now.Add(time.Second)
			result := sweepStuckBuildsWithMetrics(t.Context(), s, vm, now, slog.New(slog.NewTextHandler(io.Discard, nil)), m)
			m.finish(result)
			if (result.err != nil) != tc.wantLegacyErr {
				t.Fatalf("legacy error semantics changed: %+v", result)
			}
			if got := testutil.ToFloat64(m.swept); got != float64(tc.wantSwept) {
				t.Fatalf("recovered=%v", got)
			}
			wantSuccess := 0.0
			if tc.wantOK {
				wantSuccess = 1
			}
			if got := testutil.ToFloat64(m.lastResult); got != wantSuccess {
				t.Fatalf("whole-pass success=%v", got)
			}
			wantLast := 0.0
			if tc.wantOK {
				wantLast = float64(now.Unix())
			}
			if got := testutil.ToFloat64(m.lastSuccess); got != wantLast {
				t.Fatalf("last success=%v", got)
			}
			if tc.cancelFail {
				if s.recordedCleanupErr == nil {
					t.Fatal("cleanup failure did not remain retryable")
				}
				if got := testutil.ToFloat64(m.cleanup.WithLabelValues("cancel", "error")); got != 1 {
					t.Fatalf("cancel errors=%v", got)
				}
			}
			if tc.recordErr != nil && testutil.ToFloat64(m.cleanup.WithLabelValues("record", "error")) != 1 {
				t.Fatal("result-record failure missing")
			}
			// An empty successful pass is healthy and never invents recovered rows.
			s.swept, s.sweepErr, s.claimErr, s.recordErr, s.claims = 0, nil, nil, nil, nil
			now = now.Add(time.Minute)
			m.begin()
			m.finish(sweepStuckBuildsWithMetrics(t.Context(), s, vm, now, slog.New(slog.NewTextHandler(io.Discard, nil)), m))
			if testutil.ToFloat64(m.lastResult) != 1 || testutil.ToFloat64(m.lastSuccess) != float64(now.Unix()) || testutil.ToFloat64(m.swept) != float64(tc.wantSwept) {
				t.Fatal("idle recovery metrics incorrect")
			}
			// A later failure freezes last success while advancing completion.
			now = now.Add(time.Minute)
			m.finish(reaperPassResult{err: failed})
			if testutil.ToFloat64(m.lastSuccess) != float64(now.Add(-time.Minute).Unix()) || testutil.ToFloat64(m.lastCompleted) != float64(now.Unix()) {
				t.Fatal("failure renewed last success")
			}
			if _, err := reg.Gather(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReaperMetricsProductionLoop(t *testing.T) {
	store, ms, id := newReaperFixture(t)
	ms.SetBuildStartedAtForTest(id, time.Now().Add(-time.Hour))
	reg := prometheus.NewPedanticRegistry()
	m := NewReaperMetrics(reg, 5*time.Millisecond, 5*time.Minute)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	vm := reaperVM{cancelled: make(chan string, 1)}
	go func() {
		ReaperLoopWithMetrics(ctx, store, vm, 5*time.Millisecond, 5*time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)), m)
		close(done)
	}()
	select {
	case <-vm.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("reaper did not cancel stale VM")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reaper did not stop")
	}
	if testutil.ToFloat64(m.swept) != 1 || testutil.ToFloat64(m.passes.WithLabelValues("success")) < 1 {
		t.Fatal("production loop did not publish recovery")
	}
	families, err := reg.Gather()
	if err != nil || len(families) != 11 {
		t.Fatalf("families=%d err=%v", len(families), err)
	}
	want := map[string]float64{"builderd_build_reaper_interval_seconds": 0.005, "builderd_build_reaper_threshold_seconds": 300, "builderd_build_reaper_cleanup_budget_seconds": 3840}
	for _, family := range families {
		if value, ok := want[family.GetName()]; ok {
			if len(family.Metric) != 1 || len(family.Metric[0].Label) != 0 || family.Metric[0].GetGauge().GetValue() != value {
				t.Fatalf("effective config metric = %v", family)
			}
			delete(want, family.GetName())
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing effective config metrics: %v", want)
	}
}
