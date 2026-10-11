package alerts_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

type recordingClaimedAction struct {
	recordingActionExecutor
	fire string
}

func (a *recordingClaimedAction) ExecuteClaimed(ctx context.Context, rule state.AlertRule, fire string, observed float64, at time.Time) error {
	a.fire = fire
	return a.Execute(ctx, rule, observed, at)
}
func TestEvaluatorHandsOffCommittedFireID(t *testing.T) {
	store := state.NewMemStore()
	rule, identity := seedRuleWithAction(t, store, state.AlertActionRollback)
	dispatcher := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	action := &recordingClaimedAction{}
	evaluator, _ := makeEvaluatorWithAction(t, store, &stubPromQL{value: 10}, identity, dispatcher, action)
	stats, err := evaluator.RunOnce(context.Background())
	if err != nil || stats.ActionExecuted != 1 {
		t.Fatalf("evaluation %+v %v", stats, err)
	}
	deliveries, err := store.ListAlertDeliveriesForRule(context.Background(), rule.ID, 100, false)
	if err != nil || len(deliveries) != 1 || action.fire != deliveries[0].ID {
		t.Fatalf("handoff fire=%s deliveries=%+v %v", action.fire, deliveries, err)
	}
}
