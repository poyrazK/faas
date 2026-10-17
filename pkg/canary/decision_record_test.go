package canary

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	canarycatalog "github.com/onebox-faas/faas/pkg/api/canary"
)

// decisionAPID records every decision-carrying call (ADR-1447).
type decisionAPID struct {
	*exactRecoveryStub
	advanced  []api.RolloutDecision
	holds     []api.RolloutDecision
	holdSteps []int
	aborted   []api.RolloutDecision
}

func (a *decisionAPID) AdvanceCanaryWithDecision(ctx context.Context, id string, expectedStep int, decision api.RolloutDecision) (api.CanaryAdvanceResponse, error) {
	a.advanced = append(a.advanced, decision)
	return a.AdvanceCanary(ctx, id, expectedStep)
}

func (a *decisionAPID) RecordRolloutHold(_ context.Context, _ string, expectedStep int, decision api.RolloutDecision) (api.RecordRolloutHoldResponse, error) {
	a.holds = append(a.holds, decision)
	a.holdSteps = append(a.holdSteps, expectedStep)
	return api.RecordRolloutHoldResponse{Recorded: true}, nil
}

func (a *decisionAPID) RecoverDeploymentRolloutWithDecision(ctx context.Context, deploymentID, predecessorID, action, reason, key string, decision api.RolloutDecision) (api.RolloutTransitionResponse, error) {
	a.aborted = append(a.aborted, decision)
	return a.RecoverDeploymentRolloutAndIdempotencyKey(ctx, deploymentID, predecessorID, action, reason, key)
}

const decisionStableID = "6f1c2c3e-4c1b-4f61-9a0e-6a3f5c2d1b00"

func decisionRow(t *testing.T, now time.Time) CanaryRow {
	t.Helper()
	raw, err := json.Marshal([]canarycatalog.CustomStage{{Percent: 1, Duration: "30s"}, {Percent: 100, Duration: "0s"}})
	if err != nil {
		t.Fatal(err)
	}
	return CanaryRow{
		ID: "00000000-0000-0000-0000-000000000001", AppID: "00000000-0000-0000-0000-000000000002", AppSlug: "demo",
		CanaryPreset: "custom", CanaryStep: 0, CanaryTotalSteps: 2, TrafficPercent: 1,
		CanaryStepStarted: now.Add(-2 * time.Minute), RolloutState: "rolling_out", CanaryStages: raw,
	}
}

func quietObservation(candidate, stable HealthWindow) CircuitBreakerObservation {
	return CircuitBreakerObservation{
		Candidate: candidate, Stable: stable, StableDeploymentID: decisionStableID, HasStable: true,
		OOMSignalAvailable: true, LivenessSignalAvailable: true,
		CPURequestSignalAvailable: true, DependencySignalAvailable: true,
	}
}

func runDecisionTick(t *testing.T, observation CircuitBreakerObservation, signals []HealthSignal) *decisionAPID {
	t.Helper()
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	store := &observedCircuitBreakerStore{stubStore: &stubStore{rows: []CanaryRow{decisionRow(t, now)}, healthSignals: signals}, observation: observation}
	apid := &decisionAPID{exactRecoveryStub: &exactRecoveryStub{stubAPID: &stubAPID{}}}
	p := NewProgression(store, apid, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.Now = func() time.Time { return now }
	if _, err := p.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, records := range [][]api.RolloutDecision{apid.advanced, apid.holds, apid.aborted} {
		for _, record := range records {
			if err := record.Validate(); err != nil {
				t.Fatalf("record %+v is not valid: %v", record, err)
			}
		}
	}
	return apid
}

func TestProgressionRecordsProbeEvidenceAdvance(t *testing.T) {
	apid := runDecisionTick(t, quietObservation(HealthWindow{ProbeRequests: 30}, HealthWindow{Requests: 3, ProbeRequests: 21}), nil)
	if len(apid.advanced) != 1 || len(apid.holds) != 0 {
		t.Fatalf("advanced=%d holds=%d, want one advance", len(apid.advanced), len(apid.holds))
	}
	got := apid.advanced[0]
	if got.Rule != api.RolloutRuleAdvanceProbeEvidence || !got.ProbesIncluded || got.Step != 0 || got.TrafficPercent != 1 ||
		got.StableDeploymentID != decisionStableID || got.Candidate == nil || got.Candidate.ProbeRequests != 30 ||
		got.Stable == nil || got.Stable.ProbeRequests != 21 || got.Signals == nil || got.WindowStart == "" {
		t.Fatalf("probe evidence record = %+v", got)
	}
}

func TestProgressionRecordsHealthyAdvance(t *testing.T) {
	healthy := HealthWindow{Requests: 40, CPURequests: 40, CPUUsec: 400_000}
	apid := runDecisionTick(t, quietObservation(healthy, healthy), nil)
	if len(apid.advanced) != 1 || apid.advanced[0].Rule != api.RolloutRuleAdvanceHealthy || apid.advanced[0].Candidate.Requests != 40 {
		t.Fatalf("advanced = %+v", apid.advanced)
	}
}

func TestProgressionRecordsSampleSizeHold(t *testing.T) {
	apid := runDecisionTick(t, quietObservation(HealthWindow{Requests: 3}, HealthWindow{Requests: 3}), nil)
	if len(apid.advanced) != 0 || len(apid.holds) != 1 {
		t.Fatalf("advanced=%d holds=%d, want one hold", len(apid.advanced), len(apid.holds))
	}
	if got := apid.holds[0]; got.Rule != api.RolloutRuleHoldInsufficient || got.Kind != api.RolloutDecisionHold || apid.holdSteps[0] != 0 ||
		got.Candidate == nil || got.Candidate.Requests != 3 {
		t.Fatalf("hold record = %+v", got)
	}
}

func TestProgressionRecordsHealthAlertHold(t *testing.T) {
	healthy := HealthWindow{Requests: 40, CPURequests: 40, CPUUsec: 400_000}
	apid := runDecisionTick(t, quietObservation(healthy, healthy), []HealthSignal{{RuleID: "r1", RuleName: "customer text", Metric: "error_rate", Action: "rollback"}})
	if len(apid.advanced) != 0 || len(apid.holds) != 1 || apid.holds[0].Rule != api.RolloutRuleHoldHealthAlert {
		t.Fatalf("advanced=%d holds=%+v", len(apid.advanced), apid.holds)
	}
	if got := apid.holds[0].Summary; got == "" || containsAny(got, "customer text", "r1") {
		t.Fatalf("health alert summary leaked rule identity: %q", got)
	}
}

func TestProgressionRecordsAbortEvidence(t *testing.T) {
	apid := runDecisionTick(t, quietObservation(HealthWindow{Requests: 30, ServerErrors: 20}, HealthWindow{Requests: 30}), nil)
	if len(apid.aborted) != 1 || len(apid.advanced) != 0 {
		t.Fatalf("aborted=%d advanced=%d, want one abort", len(apid.aborted), len(apid.advanced))
	}
	if got := apid.aborted[0]; got.Rule != api.RolloutRuleAbort5xx || got.Kind != api.RolloutDecisionAbort ||
		got.Candidate.ServerErrors != 20 || got.StableDeploymentID != decisionStableID {
		t.Fatalf("abort record = %+v", got)
	}
	if apid.predecessorID != decisionStableID || apid.action != "abort" {
		t.Fatalf("abort went to %q/%q", apid.predecessorID, apid.action)
	}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
