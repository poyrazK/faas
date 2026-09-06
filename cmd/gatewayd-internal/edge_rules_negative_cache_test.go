package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestEdgeRulesEmptyHostLoadsOnceUntilInvalidated(t *testing.T) {
	const host = "empty.example.com"
	store := &fakeEdgeRuleStore{rules: map[string][]state.EdgeRule{}}
	matcher := newGatewaydEdgeRules(store, newQuietLogger(), nil, nil)
	for i := 0; i < 5; i++ {
		if matcher.MatchRoute(context.Background(), host, "/", "GET") != nil {
			t.Fatal("unexpected route")
		}
		matcher.MatchMaintenance(context.Background(), host, "/", "GET")
		matcher.MatchHeaders(context.Background(), host, "/", "GET")
		matcher.MatchBudget(context.Background(), host, "/", "GET")
	}
	if got := store.calls[host]; got != 1 {
		t.Fatalf("database reads for empty host = %d, want 1", got)
	}
	store.rules[host] = []state.EdgeRule{sampleRouteRule("new-route", 100, host, "/", nil, "target")}
	matcher.Reset()
	if rule := matcher.MatchRoute(context.Background(), host, "/", "GET"); rule == nil || rule.ID != "new-route" {
		t.Fatal("new route hidden after invalidation")
	}
	if got := store.calls[host]; got != 2 {
		t.Fatalf("database reads after invalidation = %d, want 2", got)
	}
}
func TestEdgeRulesLoaderFailureIsNotNegativeCached(t *testing.T) {
	const host = "retry.example.com"
	store := &fakeEdgeRuleStore{err: errors.New("database unavailable")}
	matcher := newGatewaydEdgeRules(store, newQuietLogger(), nil, nil)
	if matcher.MatchRoute(context.Background(), host, "/", "GET") != nil {
		t.Fatal("unexpected route")
	}
	store.err = nil
	store.rules = map[string][]state.EdgeRule{host: {sampleRouteRule("recovered", 100, host, "/", nil, "target")}}
	if rule := matcher.MatchRoute(context.Background(), host, "/", "GET"); rule == nil || rule.ID != "recovered" {
		t.Fatal("failed read was cached as no rules")
	}
}

type invalidatingEdgeRuleStore struct {
	*fakeEdgeRuleStore
	duringFirstRead func()
}

func (s *invalidatingEdgeRuleStore) MatchEdgeRulesForHost(ctx context.Context, host string) ([]state.EdgeRule, error) {
	if hook := s.duringFirstRead; hook != nil {
		s.duringFirstRead = nil
		// The first read observed no rules. A change notification arrives
		// before it returns, so publishing that old result would be stale.
		hook()
		return nil, nil
	}
	return s.fakeEdgeRuleStore.MatchEdgeRulesForHost(ctx, host)
}
func TestEdgeRulesInvalidationDuringLoadDoesNotHideNewRule(t *testing.T) {
	const host = "changing.example.com"
	store := &invalidatingEdgeRuleStore{fakeEdgeRuleStore: &fakeEdgeRuleStore{rules: map[string][]state.EdgeRule{}}}
	matcher := newGatewaydEdgeRules(store, newQuietLogger(), nil, nil)
	store.duringFirstRead = func() {
		store.rules[host] = []state.EdgeRule{sampleRouteRule("new-rule", 100, host, "/", nil, "target")}
		matcher.Reset()
	}
	matcher.MatchRoute(context.Background(), host, "/", "GET")
	if rule := matcher.MatchRoute(context.Background(), host, "/", "GET"); rule == nil || rule.ID != "new-rule" {
		t.Fatal("pre-invalidation read hid the new rule")
	}
}
