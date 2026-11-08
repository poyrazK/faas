package canary

// adr: 1446 — amendment 3: a stage nobody called advances at its boundary.

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// ChronoLake report: an internal service is never probed through the public
// edge, so each stage of its rollout waited its dwell time plus the full
// CanaryLowTrafficMaxHold and a four-stage rollout outlasted the 20-minute
// deploy wait. A stage with no request on either side, organic or probe,
// advances at its boundary; any traffic or negative evidence keeps the bound.
func TestProgressionOnce_NoTrafficStageAdvancesAtBoundary(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name         string
		elapsed      time.Duration
		candidate    HealthWindow
		stable       HealthWindow
		wantAdvanced int
		wantSkipped  int
	}{
		{name: "no requests past the dwell time advances", elapsed: 2*time.Minute + time.Second, wantAdvanced: 1},
		{name: "no requests before the dwell time holds", elapsed: time.Minute, wantSkipped: 0},
		{name: "a predecessor request keeps the bound", elapsed: 2*time.Minute + time.Second, stable: HealthWindow{Requests: 1}, wantSkipped: 1},
		{name: "a probe keeps the bound", elapsed: 2*time.Minute + time.Second, candidate: HealthWindow{ProbeRequests: 1}, wantSkipped: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &observedCircuitBreakerStore{
				stubStore: &stubStore{rows: []CanaryRow{{
					ID:                "00000000-0000-0000-0000-000000000001",
					AppID:             "00000000-0000-0000-0000-000000000002",
					CanaryPreset:      "balanced",
					CanaryStep:        0,
					CanaryTotalSteps:  4,
					CanaryStepStarted: now.Add(-tc.elapsed),
					RolloutState:      "rolling_out",
				}}},
				observation: CircuitBreakerObservation{
					Candidate:                 tc.candidate,
					Stable:                    tc.stable,
					StableDeploymentID:        "00000000-0000-0000-0000-000000000003",
					HasStable:                 true,
					OOMSignalAvailable:        true,
					LivenessSignalAvailable:   true,
					DependencySignalAvailable: true,
				},
			}
			progression := NewProgression(store, &stubAPID{}, wire.NewOpsMetrics("meterd"), slog.Default())
			progression.Now = func() time.Time { return now }

			stats, err := progression.Once(context.Background())
			if err != nil {
				t.Fatalf("Once: %v", err)
			}
			if stats.LowTrafficAdvanced != tc.wantAdvanced || stats.SkippedCircuitBreaker != tc.wantSkipped {
				t.Fatalf("stats = %+v; want low-traffic advances=%d holds=%d", stats, tc.wantAdvanced, tc.wantSkipped)
			}
		})
	}
}
