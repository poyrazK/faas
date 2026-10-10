package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-964: each rule keeps at most edgeRuleEventsPerRule events per flush
// interval, the budget resets after a flush, and flushed events land in the
// store with the request snapshot.
func TestEdgeRuleHitCounterSamplesEvents(t *testing.T) {
	const rule = "11111111-1111-1111-1111-111111111111"
	store := state.NewMemStore()
	c := newEdgeRuleHitCounter()
	req := httptest.NewRequest(http.MethodPost, "http://api.example.com/login?token=secret", nil)
	req.Header.Set("User-Agent", "curl/8")
	req.Header.Set(api.RequestIDHeader, "req-1")
	routes := []gateway.EdgeRuleRedirectResolved{{EdgeRuleCondition: gateway.EdgeRuleCondition{RuleID: rule, AppID: "app-1"}, ID: rule, AccountID: "acct", To: "/x"}}

	serve := func() {
		m := gateway.NewEdgeRuleMatchContext(req, net.ParseIP("203.0.113.9"), func(net.IP) string { return "DE" }, c)
		ctx := gateway.WithEdgeRuleMatchContext(context.Background(), m)
		gateway.ObserveEdgeRuleMatch(ctx, &routes[0], (*gateway.EdgeRuleRedirectResolved)(nil))
	}
	for range edgeRuleEventsPerRule + 5 {
		serve()
	}
	c.flush(context.Background(), store, nil)
	events, err := store.ListEdgeRuleEvents(context.Background(), state.EdgeRuleEventQuery{AppID: "app-1", Limit: 100})
	if err != nil || len(events) != edgeRuleEventsPerRule {
		t.Fatalf("events = %d, %v; want %d", len(events), err, edgeRuleEventsPerRule)
	}
	e := events[0]
	if e.RuleID != rule || e.Outcome != state.EdgeRuleHitMatched || e.Method != http.MethodPost || e.Host != "api.example.com" ||
		e.Path != "/login" || e.ClientIP != "203.0.113.9" || e.Country != "DE" || e.UserAgent != "curl/8" || e.RequestID != "req-1" {
		t.Fatalf("event = %+v", e)
	}
	stats, _ := store.EdgeRuleHitStatsForApp(context.Background(), "app-1", time.Time{})
	if len(stats) != 1 || stats[0].Matched != int64(edgeRuleEventsPerRule+5) {
		t.Fatalf("hit counts must still cover every match: %+v", stats)
	}

	serve()
	c.flush(context.Background(), store, nil)
	events, _ = store.ListEdgeRuleEvents(context.Background(), state.EdgeRuleEventQuery{AppID: "app-1", Limit: 100})
	if len(events) != edgeRuleEventsPerRule+1 {
		t.Fatalf("sampling budget did not reset after flush: %d events", len(events))
	}
}

// Free-text fields are bounded before they reach the store.
func TestEdgeRuleEventTruncatesUserAgent(t *testing.T) {
	c := newEdgeRuleHitCounter()
	req := httptest.NewRequest(http.MethodGet, "http://a.example.com/", nil)
	req.Header.Set("User-Agent", strings.Repeat("é", gateway.EdgeRuleEventMaxUserAgentBytes))
	routes := []gateway.EdgeRuleRedirectResolved{{EdgeRuleCondition: gateway.EdgeRuleCondition{RuleID: "r", AppID: "a"}, ID: "r", To: "/x"}}
	ctx := gateway.WithEdgeRuleMatchContext(context.Background(), gateway.NewEdgeRuleMatchContext(req, nil, nil, c))
	gateway.ObserveEdgeRuleMatch(ctx, &routes[0], (*gateway.EdgeRuleRedirectResolved)(nil))
	events, _ := c.drainEvents()
	if len(events) != 1 || len(events[0].UserAgent) > gateway.EdgeRuleEventMaxUserAgentBytes || !utf8.ValidString(events[0].UserAgent) || events[0].ClientIP != "" {
		t.Fatalf("events = %+v", events)
	}
}

func TestEdgeRuleEventQueueBound(t *testing.T) {
	c := newEdgeRuleHitCounter()
	for range edgeRuleEventMaxQueued + 3 {
		c.RecordEdgeRuleEvent(gateway.EdgeRuleEvent{RuleID: "r", AppID: "a"})
	}
	events, dropped := c.drainEvents()
	if len(events) != edgeRuleEventMaxQueued || dropped != 3 {
		t.Fatalf("queued %d dropped %d", len(events), dropped)
	}
}
