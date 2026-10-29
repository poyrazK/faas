package alerts_test

// adr: 20261022193512 — the evaluator's state changes open a rule's episode
// when it fires and resolve it when the rule recovers.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestEvaluator_FiringOpensAndRecoveryResolvesAnEpisode(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	store := state.NewMemStore()
	rule, _, evaluate := seedLogSearchRule(t, store, "level=error&q=timeout", 2)
	insertLogEvent(t, store, rule, now.Add(-time.Minute), "error", "db timeout", 3)
	if fired := evaluate(now); fired != 1 {
		t.Fatalf("fired = %d, want 1", fired)
	}
	eps, err := store.ListAlertEpisodes(ctx, rule.AccountID, "", now.Add(-time.Hour), 10)
	if err != nil || len(eps) != 1 || eps[0].ResolvedAt != nil || !eps[0].OpenedAt.Equal(now) || eps[0].RuleID != rule.ID {
		t.Fatalf("episodes after the fire = %+v, %v; want one open since %s", eps, err, now)
	}

	// An hour on, the matches have left the window and the rule recovers.
	later := now.Add(time.Hour)
	if fired := evaluate(later); fired != 0 {
		t.Fatalf("fired after recovery = %d, want 0", fired)
	}
	eps, err = store.ListAlertEpisodes(ctx, rule.AccountID, "", now.Add(-time.Hour), 10)
	if err != nil || len(eps) != 1 || eps[0].ResolvedAt == nil || !eps[0].ResolvedAt.Equal(later) {
		t.Fatalf("episodes after recovery = %+v, %v; want it resolved at %s", eps, err, later)
	}
}
