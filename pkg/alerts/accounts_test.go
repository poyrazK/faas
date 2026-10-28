package alerts_test

// adr: 20261028101615 — a tick evaluates accounts in parallel, so one
// account's unresponsive webhook delays only that account; an account that
// spends its tick budget resumes with the rules it deferred; and meterd's
// release decides which rules run and whether channels deliver.

import (
	"context"
	"sync"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// gatedDispatcher holds slowApp's deliveries until release is closed.
type gatedDispatcher struct {
	recordingDispatcher
	slowApp string
	release chan struct{}
}

func (g *gatedDispatcher) Dispatch(ctx context.Context, t webhookout.Target, evt webhookout.Event) webhookout.Result {
	if evt.AppID == g.slowApp {
		select {
		case <-g.release:
		case <-ctx.Done():
		}
	}
	return g.recordingDispatcher.Dispatch(ctx, t, evt)
}

func evaluatorFor(store *state.MemStore, dispatch alerts.Dispatcher, budget time.Duration, idents ...*age.X25519Identity) *alerts.Evaluator {
	return alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:             store,
		PromQL:            &stubPromQL{value: 10},
		Audit:             audit.New(store, discardLog(), nil, "meterd"),
		Identities:        func() []*age.X25519Identity { return idents },
		Dispatcher:        dispatch,
		Now:               func() time.Time { return time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) },
		Log:               discardLog(),
		AccountTickBudget: budget,
	})
}

func TestEvaluator_SlowAccountDoesNotDelayOthers(t *testing.T) {
	store := state.NewMemStore()
	slow, slowIdent, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertGt, 5)
	_, fastIdent, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertGt, 5)
	dispatch := &gatedDispatcher{recordingDispatcher: recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}},
		slowApp: slow.AppID, release: make(chan struct{})}
	ev := evaluatorFor(store, dispatch, 0, slowIdent, fastIdent)

	done := make(chan alerts.Stats, 1)
	go func() {
		stats, _ := ev.RunOnce(context.Background())
		done <- stats
	}()
	deadline := time.Now().Add(5 * time.Second)
	for dispatch.callCount() == 0 {
		if time.Now().After(deadline) {
			close(dispatch.release)
			t.Fatal("the other account's alert was not delivered while one account's webhook hung")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(dispatch.release)
	if stats := <-done; stats.Fired != 2 || stats.Delivered != 2 {
		t.Fatalf("stats = %+v, want both accounts fired and delivered", stats)
	}
}

func TestEvaluator_AccountTickBudgetDefersAndResumes(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	first, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertGt, 5)
	ids := []string{first.ID}
	for _, name := range []string{"second", "third"} {
		next := first
		next.ID, next.Name = "", first.Name+"-"+name
		rule, err := store.CreateAlertRule(ctx, next)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, rule.ID)
	}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	// Any elapsed time spends a nanosecond budget, so each tick evaluates
	// exactly one rule before deferring the rest.
	ev := evaluatorFor(store, dispatch, time.Nanosecond, ident)
	for tick := 1; tick <= 3; tick++ {
		stats, err := ev.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Fired != 1 || stats.Deferred != 2 {
			t.Fatalf("tick %d stats = %+v, want one rule fired and two deferred", tick, stats)
		}
	}
	for _, id := range ids {
		rule, err := store.AlertRuleByID(ctx, id)
		if err != nil || rule.State != state.AlertStateFiring {
			t.Fatalf("rule %s = %q (%v): every rule must be reached once the budget defers the rest", id, rule.State, err)
		}
	}
}

func TestEvaluator_RuleReleaseSkipsRulesAndChannels(t *testing.T) {
	store := state.NewMemStore()
	quiet, quietIdent := seedChannelRule(t, store)
	withheld, withheldIdent, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertGt, 5)
	channels := &recordingChannels{}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	ev := evaluatorFor(store, dispatch, 0, quietIdent, withheldIdent)
	ev.SetChannels(channels, nil)
	ev.SetRuleRelease(func(_ context.Context, accountID string) alerts.RuleRelease {
		switch accountID {
		case quiet.AccountID:
			return func(context.Context, state.AlertRule) (bool, bool) { return true, false }
		case withheld.AccountID:
			return func(context.Context, state.AlertRule) (bool, bool) { return false, false }
		}
		return nil
	})
	var mu sync.Mutex
	skipped := map[string]int{}
	ev.SetRuleOutcome(func(reason string, n int) {
		mu.Lock()
		defer mu.Unlock()
		skipped[reason] += n
	})

	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Fired != 1 || stats.SkippedNotReleased != 1 || skipped["not_released"] != 1 {
		t.Fatalf("stats = %+v, skipped = %v; want the released rule fired and the other skipped", stats, skipped)
	}
	if len(channels.sent) != 0 {
		t.Fatalf("channels received %d notifications; the account's channels are not released", len(channels.sent))
	}
	got, err := store.AlertRuleByID(context.Background(), withheld.ID)
	if err != nil || got.State == state.AlertStateFiring {
		t.Fatalf("withheld rule state = %q (%v), want it left unevaluated", got.State, err)
	}
}
