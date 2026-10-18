package alerts_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

const (
	edgeRuleA = "11111111-1111-4111-8111-111111111111"
	edgeRuleB = "22222222-2222-4222-8222-222222222222"
)

// adr: 1192 — edge-rule activity alerts count the ADR-960 hit buckets:
// enforced matches or log-mode matches, for one rule or the whole app, and
// the webhook carries the rule id.
func TestEvaluatorEdgeRuleActivity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		metric   state.AlertMetric
		ruleID   string
		wantFire bool
	}{
		{"app-wide enforced 8+5 > 10", state.AlertMetricEdgeRuleHits, "", true},
		{"rule A enforced 8 <= 10", state.AlertMetricEdgeRuleHits, edgeRuleA, false},
		{"rule A logged 50 > 10", state.AlertMetricEdgeRuleLogHits, edgeRuleA, true},
		{"rule B logged 0 <= 10", state.AlertMetricEdgeRuleLogHits, edgeRuleB, false},
		{"deleted rule counts zero", state.AlertMetricEdgeRuleHits, "33333333-3333-4333-8333-333333333333", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			seeded, ident, _ := seedRule(t, store, tc.metric, state.AlertGt, 10)
			rule := seeded
			if tc.ruleID != "" {
				if err := store.DeleteAlertRule(context.Background(), seeded.ID); err != nil {
					t.Fatal(err)
				}
				seeded.ID, seeded.Name, seeded.EdgeRuleID = "", seeded.Name+"-scoped", tc.ruleID
				var err error
				if rule, err = store.CreateAlertRule(context.Background(), seeded); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now()
			if err := store.RecordEdgeRuleHits(context.Background(), []state.EdgeRuleHit{
				{RuleID: edgeRuleA, AppID: rule.AppID, Bucket: now, Outcome: state.EdgeRuleHitMatched, Hits: 8},
				{RuleID: edgeRuleB, AppID: rule.AppID, Bucket: now, Outcome: state.EdgeRuleHitMatched, Hits: 5},
				{RuleID: edgeRuleA, AppID: rule.AppID, Bucket: now, Outcome: state.EdgeRuleHitLogged, Hits: 50},
				{RuleID: edgeRuleA, AppID: "other-app", Bucket: now, Outcome: state.EdgeRuleHitMatched, Hits: 1000},
			}); err != nil {
				t.Fatal(err)
			}
			dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
			evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: store, Audit: audit.New(store, discardLog(), nil, "meterd"), Identity: func() *age.X25519Identity { return ident }, Dispatcher: dispatch, Log: discardLog()})
			stats, err := evaluator.RunOnce(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if fired := stats.Fired == 1; fired != tc.wantFire {
				t.Fatalf("fired=%v want %v (stats=%+v)", fired, tc.wantFire, stats)
			}
			if tc.wantFire && tc.ruleID != "" {
				if got := dispatch.calls[0].Payload["edge_rule_id"]; got != tc.ruleID {
					t.Fatalf("payload edge_rule_id = %v, want %s", got, tc.ruleID)
				}
			}
		})
	}
}

// failingEdgeRuleHitStore keeps the store in a named field (statetest).
type failingEdgeRuleHitStore struct{ MemStore *state.MemStore }

func (*failingEdgeRuleHitStore) EdgeRuleHitStatsForApp(context.Context, string, time.Time) ([]state.EdgeRuleHitStats, error) {
	return nil, errors.New("database unavailable")
}

// adr: 1192 — an unreadable hit store marks the rule degraded instead of
// firing or clearing it.
func TestEvaluatorEdgeRuleActivityDegraded(t *testing.T) {
	base := state.NewMemStore()
	rule, ident, _ := seedRule(t, base, state.AlertMetricEdgeRuleHits, state.AlertGte, 0)
	dispatch := &recordingDispatcher{}
	evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: statetest.NewMem(&failingEdgeRuleHitStore{MemStore: base}), Audit: audit.New(base, discardLog(), nil, "meterd"), Identity: func() *age.X25519Identity { return ident }, Dispatcher: dispatch, Log: discardLog()})
	stats, err := evaluator.RunOnce(context.Background())
	if err != nil || stats.Fired != 0 || dispatch.callCount() != 0 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	stored, err := base.AlertRuleByID(context.Background(), rule.ID)
	if err != nil || stored.State != state.AlertStateDegraded {
		t.Fatalf("rule=%+v err=%v", stored, err)
	}
}
