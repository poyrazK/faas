package canary

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// One default probe round must meet the request floor, or a quiet stage
// waits for a second round (ADR-1321).
func TestDefaultProbeRoundMeetsRequestFloor(t *testing.T) {
	if api.DefaultRouteProbeRequestsPerRound < CircuitBreakerMinRequests {
		t.Fatalf("DefaultRouteProbeRequestsPerRound = %d, below the %d-request floor",
			api.DefaultRouteProbeRequestsPerRound, CircuitBreakerMinRequests)
	}
}

// TestProgressionEarlyAdvance pins ADR-1321: an adaptive stage advances
// before its 2-minute timer only on conclusive, clean evidence after one
// minute; everything else keeps the timer.
func TestProgressionEarlyAdvance(t *testing.T) {
	healthy := HealthWindow{Requests: 40, CPURequests: 40, CPUUsec: 400_000}
	probed := quietObservation(HealthWindow{ProbeRequests: 20}, HealthWindow{Requests: 3, ProbeRequests: 20})
	restarted := probed
	restarted.CandidateLivenessRestarts = 1
	noPredecessor := CircuitBreakerObservation{OOMSignalAvailable: true}
	alert := []HealthSignal{{RuleID: "r1", Metric: "error_rate", Action: "rollback"}}

	cases := []struct {
		name        string
		preset      string
		elapsed     time.Duration
		observation CircuitBreakerObservation
		signals     []HealthSignal
		wantRule    string // advance rule, or "" for no advance
		wantHold    string // recorded hold rule, if any
		wantAbort   bool
	}{
		{name: "clean organic sample after a minute", preset: "adaptive", elapsed: 70 * time.Second,
			observation: quietObservation(healthy, healthy), wantRule: api.RolloutRuleAdvanceHealthy},
		{name: "clean probe evidence after a minute", preset: "adaptive", elapsed: 70 * time.Second,
			observation: probed, wantRule: api.RolloutRuleAdvanceProbeEvidence},
		{name: "clean evidence before the minute waits", preset: "adaptive", elapsed: 50 * time.Second,
			observation: quietObservation(healthy, healthy)},
		{name: "too few samples waits without a hold", preset: "adaptive", elapsed: 70 * time.Second,
			observation: quietObservation(HealthWindow{Requests: 3}, HealthWindow{Requests: 3})},
		{name: "a liveness restart keeps the timer", preset: "adaptive", elapsed: 70 * time.Second,
			observation: restarted},
		{name: "no predecessor keeps the timer", preset: "adaptive", elapsed: 70 * time.Second,
			observation: noPredecessor},
		{name: "explicit balanced keeps the timer", preset: "balanced", elapsed: 70 * time.Second,
			observation: quietObservation(healthy, healthy)},
		{name: "a firing health alert still holds", preset: "adaptive", elapsed: 70 * time.Second,
			observation: quietObservation(healthy, healthy), signals: alert, wantHold: api.RolloutRuleHoldHealthAlert},
		{name: "a clear regression aborts at any time", preset: "adaptive", elapsed: 20 * time.Second,
			observation: quietObservation(HealthWindow{Requests: 30, ServerErrors: 20}, HealthWindow{Requests: 30}), wantAbort: true},
		{name: "too few samples at the timer holds as before", preset: "adaptive", elapsed: 130 * time.Second,
			observation: quietObservation(HealthWindow{Requests: 3}, HealthWindow{Requests: 3}), wantHold: api.RolloutRuleHoldInsufficient},
		{name: "clean evidence at the timer advances as before", preset: "balanced", elapsed: 130 * time.Second,
			observation: quietObservation(healthy, healthy), wantRule: api.RolloutRuleAdvanceHealthy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
			row := CanaryRow{
				ID: "00000000-0000-0000-0000-000000000001", AppID: "00000000-0000-0000-0000-000000000002", AppSlug: "demo",
				CanaryPreset: tc.preset, CanaryStep: 0, CanaryTotalSteps: 4, TrafficPercent: 1,
				CanaryStepStarted: now.Add(-tc.elapsed), RolloutStarted: now.Add(-tc.elapsed), RolloutState: "rolling_out",
			}
			store := &observedCircuitBreakerStore{stubStore: &stubStore{rows: []CanaryRow{row}, healthSignals: tc.signals}, observation: tc.observation}
			apid := &decisionAPID{exactRecoveryStub: &exactRecoveryStub{stubAPID: &stubAPID{}}}
			p := NewProgression(store, apid, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			p.Now = func() time.Time { return now }
			stats, err := p.Once(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			if tc.wantRule == "" {
				if len(apid.advanced) != 0 || stats.Advanced != 0 {
					t.Fatalf("advanced %+v (stats %+v), want no advance", apid.advanced, stats)
				}
			} else {
				if len(apid.advanced) != 1 || apid.advanced[0].Rule != tc.wantRule || stats.Advanced != 1 {
					t.Fatalf("advanced %+v (stats %+v), want one %s advance", apid.advanced, stats, tc.wantRule)
				}
				if err := apid.advanced[0].Validate(); err != nil {
					t.Fatalf("advance record invalid: %v", err)
				}
				if wantEarly := tc.elapsed < 2*time.Minute; (stats.EarlyAdvanced == 1) != wantEarly {
					t.Fatalf("EarlyAdvanced = %d, want early=%v", stats.EarlyAdvanced, wantEarly)
				}
			}
			switch {
			case tc.wantHold == "" && len(apid.holds) != 0:
				t.Fatalf("holds %+v, want none", apid.holds)
			case tc.wantHold != "" && (len(apid.holds) != 1 || apid.holds[0].Rule != tc.wantHold):
				t.Fatalf("holds %+v, want one %s", apid.holds, tc.wantHold)
			}
			if gotAbort := len(apid.aborted) == 1; gotAbort != tc.wantAbort {
				t.Fatalf("aborted %+v, want abort=%v", apid.aborted, tc.wantAbort)
			}
		})
	}
}
