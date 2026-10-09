package state

import (
	"context"
	"testing"
	"time"
)

// ADR-834: events read newest first, filter by rule and outcome, page by
// (occurred_at, id), and prune by age.
func TestMemStoreEdgeRuleEvents(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	base := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	var events []EdgeRuleEvent
	for i := range 5 {
		outcome := EdgeRuleHitMatched
		if i%2 == 1 {
			outcome = EdgeRuleHitLogged
		}
		events = append(events, EdgeRuleEvent{RuleID: "r1", AppID: "app", OccurredAt: base.Add(time.Duration(i) * time.Minute), Outcome: outcome, ClientIP: "not-an-ip"})
	}
	events = append(events, EdgeRuleEvent{RuleID: "r2", AppID: "app", OccurredAt: base, Outcome: EdgeRuleHitMatched},
		EdgeRuleEvent{RuleID: "r1", AppID: "other", OccurredAt: base, Outcome: EdgeRuleHitMatched})
	if err := m.RecordEdgeRuleEvents(ctx, events); err != nil {
		t.Fatal(err)
	}

	page, _ := m.ListEdgeRuleEvents(ctx, EdgeRuleEventQuery{AppID: "app", RuleID: "r1", Limit: 2})
	if len(page) != 2 || !page[0].OccurredAt.Equal(base.Add(4*time.Minute)) || page[0].ClientIP != "" {
		t.Fatalf("first page = %+v", page)
	}
	next, _ := m.ListEdgeRuleEvents(ctx, EdgeRuleEventQuery{AppID: "app", RuleID: "r1", Limit: 10, BeforeAt: page[1].OccurredAt, BeforeID: page[1].ID})
	if len(next) != 3 || !next[0].OccurredAt.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("second page = %+v", next)
	}
	logged, _ := m.ListEdgeRuleEvents(ctx, EdgeRuleEventQuery{AppID: "app", Outcome: EdgeRuleHitLogged, Limit: 10})
	if len(logged) != 2 {
		t.Fatalf("logged = %d, want 2", len(logged))
	}
	recent, _ := m.ListEdgeRuleEvents(ctx, EdgeRuleEventQuery{AppID: "app", Since: base.Add(3 * time.Minute), Limit: 10})
	if len(recent) != 2 {
		t.Fatalf("since filter = %d, want 2", len(recent))
	}

	n, _ := m.PruneEdgeRuleEvents(ctx, base.Add(time.Minute))
	if n != 3 {
		t.Fatalf("pruned %d, want 3", n)
	}
}
