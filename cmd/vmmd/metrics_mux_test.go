package main

import (
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/wire"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsMuxCombinesWakeExecutionAndEventWrites(t *testing.T) {
	ops := wire.NewOpsMetrics("vmmd")
	phases := fcvm.NewWakePhaseMetrics()
	phases.ObserveWakePhase("restore_ms", 125)
	ops.WakePhaseDuration("restore_breakdown", "ok").Observe(0.002)
	mux := newMetricsMux(ops, fcvm.NewColdBootMetrics(), fcvm.NewFrameworkReadyMetrics(), phases)
	for attempt := 0; attempt < 2; attempt++ {
		for _, path := range []string{"/metrics", "/metrics/fallback", "/metrics/framework-warmup", "/metrics/wake-phase"} {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("attempt %d %s: status %d: %s", attempt, path, rec.Code, rec.Body.String())
			}
			if path != "/metrics" {
				continue
			}
			for _, series := range []string{
				`vmmd_wake_phase_duration_seconds_sum{phase="restore_ms"} 0.125`,
				`vmmd_wake_event_write_duration_seconds_sum{phase="restore_breakdown",result="ok"} 0.002`,
			} {
				if !strings.Contains(rec.Body.String(), series) {
					t.Errorf("missing series %s", series)
				}
			}
		}
	}
}
