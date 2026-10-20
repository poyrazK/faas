package canary

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/releasegate"
)

// adr: 20261019071734 — a failing release gate aborts the canary through the
// predecessor restore; gates never hold a rollout on missing evidence.

type gatedCircuitBreakerStore struct {
	*observedCircuitBreakerStore
	verdict releasegate.Verdict
	err     error
	stable  string
}

func (g *gatedCircuitBreakerStore) ReleaseGateVerdict(_ context.Context, _ CanaryRow, stableDeploymentID string, _ time.Time) (releasegate.Verdict, error) {
	g.stable = stableDeploymentID
	return g.verdict, g.err
}

func runGatedTick(t *testing.T, store *gatedCircuitBreakerStore) *decisionAPID {
	t.Helper()
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	store.observedCircuitBreakerStore.stubStore.rows = []CanaryRow{decisionRow(t, now)}
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

func gatedStore(verdict releasegate.Verdict, err error) *gatedCircuitBreakerStore {
	healthy := quietObservation(HealthWindow{Requests: 400}, HealthWindow{Requests: 400})
	return &gatedCircuitBreakerStore{
		observedCircuitBreakerStore: &observedCircuitBreakerStore{stubStore: &stubStore{}, observation: healthy},
		verdict:                     verdict, err: err,
	}
}

func TestProgressionAbortsOnAFailingReleaseGate(t *testing.T) {
	failed := api.ReleaseGateResult{Name: "checkout-slo", Kind: "slo", Observed: 20, Bound: 10, Samples: 400,
		Summary: "burn rate 20.0 (max 10) over 400 requests; 2.00% bad vs 0.00% before"}
	store := gatedStore(releasegate.Verdict{Results: []api.ReleaseGateResult{failed}, Failed: &failed}, nil)
	apid := runGatedTick(t, store)
	if len(apid.aborted) != 1 || len(apid.advanced) != 0 {
		t.Fatalf("aborted=%d advanced=%d, want one abort", len(apid.aborted), len(apid.advanced))
	}
	got := apid.aborted[0]
	if got.Rule != api.RolloutRuleAbortReleaseGate || len(got.Gates) != 1 || got.Gates[0].Name != "checkout-slo" || got.Gates[0].Passed {
		t.Fatalf("abort record = %+v", got)
	}
	if got.Summary != "release gate checkout-slo: "+failed.Summary {
		t.Fatalf("summary = %q", got.Summary)
	}
	if apid.predecessorID != decisionStableID || apid.action != "abort" || store.stable != decisionStableID {
		t.Fatalf("abort went to %q/%q; gates judged against %q", apid.predecessorID, apid.action, store.stable)
	}
}

func TestProgressionKeepsGoingWhenGatesPassOrCannotBeRead(t *testing.T) {
	passed := api.ReleaseGateResult{Name: "orders", Kind: "custom_metric", Passed: true, Observed: 9, Bound: 5, Samples: 10, Summary: "orders averaged 9"}
	for _, tc := range []struct {
		name  string
		store *gatedCircuitBreakerStore
	}{
		{"passing gate", gatedStore(releasegate.Verdict{Results: []api.ReleaseGateResult{passed}}, nil)},
		{"unreadable gates", gatedStore(releasegate.Verdict{}, errors.New("gate store down"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			apid := runGatedTick(t, tc.store)
			if len(apid.aborted) != 0 {
				t.Fatalf("aborted on %s: %+v", tc.name, apid.aborted)
			}
		})
	}
}
