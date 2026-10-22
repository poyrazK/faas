package state_test

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

// ADR-964: events read newest first, filter by rule and outcome, page by
// (occurred_at, id), and prune by age.
func TestMemStoreEdgeRuleEvents(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	base := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	var events []state.EdgeRuleEvent
	for i := range 5 {
		outcome := state.EdgeRuleHitMatched
		if i%2 == 1 {
			outcome = state.EdgeRuleHitLogged
		}
		events = append(events, state.EdgeRuleEvent{RuleID: "r1", AppID: "app", OccurredAt: base.Add(time.Duration(i) * time.Minute), Outcome: outcome, ClientIP: "not-an-ip"})
	}
	events = append(events, state.EdgeRuleEvent{RuleID: "r2", AppID: "app", OccurredAt: base, Outcome: state.EdgeRuleHitMatched},
		state.EdgeRuleEvent{RuleID: "r1", AppID: "other", OccurredAt: base, Outcome: state.EdgeRuleHitMatched})
	if err := m.RecordEdgeRuleEvents(ctx, events); err != nil {
		t.Fatal(err)
	}

	page, _ := m.ListEdgeRuleEvents(ctx, state.EdgeRuleEventQuery{AppID: "app", RuleID: "r1", Limit: 2})
	if len(page) != 2 || !page[0].OccurredAt.Equal(base.Add(4*time.Minute)) || page[0].ClientIP != "" {
		t.Fatalf("first page = %+v", page)
	}
	next, _ := m.ListEdgeRuleEvents(ctx, state.EdgeRuleEventQuery{AppID: "app", RuleID: "r1", Limit: 10, BeforeAt: page[1].OccurredAt, BeforeID: page[1].ID})
	if len(next) != 3 || !next[0].OccurredAt.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("second page = %+v", next)
	}
	logged, _ := m.ListEdgeRuleEvents(ctx, state.EdgeRuleEventQuery{AppID: "app", Outcome: state.EdgeRuleHitLogged, Limit: 10})
	if len(logged) != 2 {
		t.Fatalf("logged = %d, want 2", len(logged))
	}
	recent, _ := m.ListEdgeRuleEvents(ctx, state.EdgeRuleEventQuery{AppID: "app", Since: base.Add(3 * time.Minute), Limit: 10})
	if len(recent) != 2 {
		t.Fatalf("since filter = %d, want 2", len(recent))
	}

	n, _ := m.PruneEdgeRuleEvents(ctx, base.Add(time.Minute))
	if n != 3 {
		t.Fatalf("pruned %d, want 3", n)
	}
}

// ADR-831 amendment 5: kind=waf detections are events with WAF detail.
func TestMemStoreEdgeRuleEventsWAFDetail(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	ids := []int{942100, 942190}
	if err := m.RecordEdgeRuleEvents(ctx, []state.EdgeRuleEvent{
		{RuleID: "waf", AppID: "app", OccurredAt: at, Outcome: state.EdgeRuleEventBlocked, WAFRuleIDs: ids, WAFCategories: []string{"sqli"}, WAFScore: 10},
		{RuleID: "waf", AppID: "app", OccurredAt: at.Add(-time.Minute), Outcome: state.EdgeRuleEventDetected},
	}); err != nil {
		t.Fatal(err)
	}
	ids[0] = 1 // the store keeps its own copy
	blocked, _ := m.ListEdgeRuleEvents(ctx, state.EdgeRuleEventQuery{AppID: "app", Outcome: state.EdgeRuleEventBlocked, Limit: 10})
	if len(blocked) != 1 || blocked[0].WAFScore != 10 || len(blocked[0].WAFRuleIDs) != 2 || blocked[0].WAFRuleIDs[0] != 942100 || blocked[0].WAFCategories[0] != "sqli" {
		t.Fatalf("blocked = %+v", blocked)
	}
	for outcome, want := range map[string]bool{
		state.EdgeRuleHitMatched: true, state.EdgeRuleHitLogged: true, state.EdgeRuleEventDetected: true,
		state.EdgeRuleEventWarned: true, state.EdgeRuleEventBlocked: true, "denied": false, "": false,
	} {
		if got := state.IsEdgeRuleEventOutcome(outcome); got != want {
			t.Errorf("IsEdgeRuleEventOutcome(%q) = %v, want %v", outcome, got, want)
		}
	}
}
