package alerts_test

import (
	"context"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// actionCheckingDispatcher records whether the rule's action had already run
// when the webhook was dispatched.
type actionCheckingDispatcher struct {
	action      *recordingActionExecutor
	actionFirst bool
}

func (d *actionCheckingDispatcher) Dispatch(context.Context, webhookout.Target, webhookout.Event) webhookout.Result {
	d.action.mu.Lock()
	d.actionFirst = d.action.calls == 1
	d.action.mu.Unlock()
	return webhookout.Result{Err: context.DeadlineExceeded, Attempts: 5}
}

// A rollback rule's action ran only after its webhook returned, so a
// receiver that never answered held the rollback for the whole webhook
// budget (bug hunt H78). The action now runs first, and a failing webhook
// still does not stop it.
func TestEvaluator_ActionRunsBeforeTheWebhook(t *testing.T) {
	store := state.NewMemStore()
	rule, ident := seedRuleWithAction(t, store, state.AlertActionRollback)
	actionExec := &recordingActionExecutor{}
	dispatch := &actionCheckingDispatcher{action: actionExec}
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:      store,
		PromQL:     &stubPromQL{value: 10},
		Audit:      audit.New(store, discardLog(), nil, "meterd"),
		Identity:   func() *age.X25519Identity { return ident },
		Dispatcher: dispatch,
		ActionExec: actionExec,
		Now:        func() time.Time { return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) },
		Log:        discardLog(),
		Ops:        &stubOps{},
	})
	if _, err := ev.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if actionExec.calls != 1 || actionExec.lastRuleID != rule.ID {
		t.Fatalf("action calls = %d for %q, want 1 for the rule", actionExec.calls, actionExec.lastRuleID)
	}
	if !dispatch.actionFirst {
		t.Fatal("the webhook was dispatched before the rule's rollback ran")
	}
}
