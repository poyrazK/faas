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
	"github.com/onebox-faas/faas/pkg/webhookout"
)

type workflowSignalStore struct {
	*state.MemStore
	snapshot state.WorkflowAlertSnapshot
	err      error
}

func (s *workflowSignalStore) WorkflowAlertSnapshot(context.Context, string, string, time.Time, time.Time) (state.WorkflowAlertSnapshot, error) {
	return s.snapshot, s.err
}

func TestEvaluatorWorkflowMetricsUseDurableSignals(t *testing.T) {
	for _, metric := range []state.AlertMetric{state.AlertMetricWorkflowFailures, state.AlertMetricWorkflowQuotaSkips, state.AlertMetricWorkflowPendingAge, state.AlertMetricWorkflowWaitingAge, state.AlertMetricWorkflowDueAge} {
		t.Run(string(metric), func(t *testing.T) {
			base := state.NewMemStore()
			_, ident, _ := seedRule(t, base, metric, state.AlertGt, 10)
			store := &workflowSignalStore{MemStore: base, snapshot: state.WorkflowAlertSnapshot{Failures: 11, QuotaSkips: 12, PendingAgeSeconds: 13, WaitingAgeSeconds: 14, DueAgeSeconds: 15}}
			dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
			evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: store, Audit: audit.New(base, discardLog(), nil, "meterd"), Identity: func() *age.X25519Identity { return ident }, Dispatcher: dispatch, Log: discardLog()})
			stats, err := evaluator.RunOnce(context.Background())
			if err != nil || stats.Fired != 1 || stats.Delivered != 1 {
				t.Fatalf("stats=%+v err=%v", stats, err)
			}
		})
	}
}

func TestEvaluatorWorkflowSignalFailureIsDegraded(t *testing.T) {
	base := state.NewMemStore()
	rule, ident, _ := seedRule(t, base, state.AlertMetricWorkflowDueAge, state.AlertGt, 0)
	store := &workflowSignalStore{MemStore: base, err: errors.New("database unavailable")}
	dispatch := &recordingDispatcher{}
	evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: store, Audit: audit.New(base, discardLog(), nil, "meterd"), Identity: func() *age.X25519Identity { return ident }, Dispatcher: dispatch, Log: discardLog()})
	stats, err := evaluator.RunOnce(context.Background())
	if err != nil || stats.Fired != 0 || dispatch.callCount() != 0 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	stored, err := base.AlertRuleByID(context.Background(), rule.ID)
	if err != nil || stored.State != state.AlertStateDegraded {
		t.Fatalf("rule=%+v err=%v", stored, err)
	}
}
