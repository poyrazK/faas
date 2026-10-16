package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-964 over HTTP: events read newest first with the rule's name, filter by
// rule and outcome, page by cursor, and never reach past the plan window.
func TestEdgeRuleEventsAPI(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	slug := apidserver.MustSeedEdgeRuleApp(t, e, "events")
	req := apidserver.EdgeRuleRouteReq("canary")
	req.MatchHost = "api.example.com"
	req.Name = "block-scrapers"
	rec := e.Do(t, "POST", "/v1/apps/"+slug+"/edge-rules", req, nil)
	var rule api.EdgeRuleResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &rule)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rule: %d %s", rec.Code, rec.Body.String())
	}

	now := time.Now().UTC()
	var events []state.EdgeRuleEvent
	for i := range 3 {
		events = append(events, state.EdgeRuleEvent{RuleID: rule.ID, AppID: rule.AppID, OccurredAt: now.Add(-time.Duration(i) * time.Minute),
			Outcome: state.EdgeRuleHitMatched, Method: "GET", Host: "api.example.com", Path: "/x", ClientIP: "203.0.113.9", Country: "DE"})
	}
	events = append(events,
		state.EdgeRuleEvent{RuleID: rule.ID, AppID: rule.AppID, OccurredAt: now.Add(-10 * time.Minute), Outcome: state.EdgeRuleHitLogged},
		// Outside the Free plan's 24 h window.
		state.EdgeRuleEvent{RuleID: rule.ID, AppID: rule.AppID, OccurredAt: now.Add(-48 * time.Hour), Outcome: state.EdgeRuleHitMatched})
	var store state.EdgeRuleEventStore = (*e.Store())
	if err := store.RecordEdgeRuleEvents(context.Background(), events); err != nil {
		t.Fatal(err)
	}

	read := func(query string) api.EdgeRuleEventsResponse {
		t.Helper()
		rec := e.Do(t, "GET", "/v1/apps/"+slug+"/edge-rules/events"+query, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("events%s: %d %s", query, rec.Code, rec.Body.String())
		}
		var out api.EdgeRuleEventsResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	page := read("?since=7d&limit=2")
	if len(page.Events) != 2 || page.NextCursor == "" || page.Events[0].RuleName != "block-scrapers" ||
		page.Events[0].ClientIP != "203.0.113.9" || now.Sub(page.Since) > 25*time.Hour {
		t.Fatalf("first page = %+v", page)
	}
	rest := read("?since=7d&limit=10&cursor=" + page.NextCursor)
	if len(rest.Events) != 2 || rest.NextCursor != "" {
		t.Fatalf("second page = %d events, cursor %q; want 2 and none (48h-old event is outside the Free window)", len(rest.Events), rest.NextCursor)
	}
	if logged := read("?outcome=logged&rule=" + rule.ID); len(logged.Events) != 1 {
		t.Fatalf("logged = %+v", logged.Events)
	}
	// ADR-831 amendment 5: kind=waf detections are events with WAF detail.
	if err := store.RecordEdgeRuleEvents(context.Background(), []state.EdgeRuleEvent{{
		RuleID: rule.ID, AppID: rule.AppID, OccurredAt: now, Outcome: state.EdgeRuleEventBlocked, Method: "GET", Path: "/login",
		WAFRuleIDs: []int{942100}, WAFCategories: []string{"sqli"}, WAFScore: 5,
	}}); err != nil {
		t.Fatal(err)
	}
	blocked := read("?outcome=blocked")
	if len(blocked.Events) != 1 || blocked.Events[0].WAFScore != 5 || len(blocked.Events[0].WAFRuleIDs) != 1 ||
		blocked.Events[0].WAFRuleIDs[0] != 942100 || blocked.Events[0].WAFCategories[0] != "sqli" {
		t.Fatalf("blocked = %+v", blocked.Events)
	}
	for _, bad := range []string{"?since=-1h", "?since=soon", "?limit=500", "?outcome=denied", "?rule=nope", "?cursor=x"} {
		if rec := e.Do(t, "GET", "/v1/apps/"+slug+"/edge-rules/events"+bad, nil, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, rec.Code)
		}
	}
}
