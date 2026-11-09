package alerts_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// blockingActionExecutor holds the action of one rule until released and
// reports every other rule's action as it runs.
type blockingActionExecutor struct {
	blocked string
	release chan struct{}
	ran     chan string
}

func (b *blockingActionExecutor) Execute(ctx context.Context, rule state.AlertRule, _ float64, _ time.Time) error {
	if rule.ID == b.blocked {
		select {
		case <-b.release:
		case <-ctx.Done():
		}
		return nil
	}
	b.ran <- rule.ID
	return nil
}

// adr: 20261109120000 — one account's slow action (a rollback waiting on
// apid) does not hold up another account's fires: actions are no longer
// serialised behind one evaluator-wide lock.
func TestEvaluator_SlowActionDoesNotBlockOtherAccounts(t *testing.T) {
	store := state.NewMemStore()
	slow, ident := seedRuleWithAction(t, store, state.AlertActionRollback)
	other, _, _ := seedRuleForApp(t, store, "other-app", state.AlertMetricErrorRate, state.AlertGt, 5)
	rollback := string(state.AlertActionRollback)
	if _, err := store.UpdateAlertRule(context.Background(), other.ID, state.UpdateAlertRuleParams{Action: &rollback}); err != nil {
		t.Fatal(err)
	}
	exec := &blockingActionExecutor{blocked: slow.ID, release: make(chan struct{}), ran: make(chan string, 1)}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	ev, _ := makeEvaluatorWithAction(t, store, &stubPromQL{value: 10}, ident, dispatch, exec)

	done := make(chan error, 1)
	go func() {
		_, err := ev.RunOnce(context.Background())
		done <- err
	}()
	select {
	case id := <-exec.ran:
		if id != other.ID {
			t.Fatalf("ran %s, want the other account's rule %s", id, other.ID)
		}
	case <-time.After(10 * time.Second):
		close(exec.release)
		t.Fatal("the other account's action waited for the blocked rollback")
	}
	close(exec.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
