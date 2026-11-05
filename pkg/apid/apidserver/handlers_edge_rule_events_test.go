package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
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
	// ADR-831 amendment 7 follow-up: kind=challenge outcomes are events too.
	if err := store.RecordEdgeRuleEvents(context.Background(), []state.EdgeRuleEvent{{
		RuleID: rule.ID, AppID: rule.AppID, OccurredAt: now, Outcome: state.EdgeRuleEventRejected, Method: "GET", Path: "/api/cart",
		ClientIP: "198.51.100.23", UserAgent: "python-requests/2.32",
	}, {
		RuleID: rule.ID, AppID: rule.AppID, OccurredAt: now, Outcome: state.EdgeRuleEventChallenged, Method: "GET", Path: "/shop",
		RequestID: "req-no-js-7f3a", ClientIP: "198.51.100.77",
	}}); err != nil {
		t.Fatal(err)
	}
	// ADR-831 amendment 8: the owner finds the reference a visitor without
	// JavaScript was shown.
	if ref := read("?request_id=req-no-js-7f3a"); len(ref.Events) != 1 || ref.Events[0].ClientIP != "198.51.100.77" || ref.Events[0].Outcome != state.EdgeRuleEventChallenged {
		t.Fatalf("request_id = %+v", ref.Events)
	}
	if rejected := read("?outcome=rejected"); len(rejected.Events) != 1 || rejected.Events[0].Path != "/api/cart" || rejected.Events[0].UserAgent != "python-requests/2.32" {
		t.Fatalf("rejected = %+v", rejected.Events)
	}
	if solved := read("?outcome=solved"); len(solved.Events) != 0 {
		t.Fatalf("solved = %+v, want none", solved.Events)
	}
	// ADR-20261022105846 amendment 2: a penalty box is an event too.
	if err := store.RecordEdgeRuleEvents(context.Background(), []state.EdgeRuleEvent{{
		RuleID: rule.ID, AppID: rule.AppID, OccurredAt: now, Outcome: state.EdgeRuleEventBoxed, Method: "GET", Path: "/xmlrpc.php",
		ClientIP: "203.0.113.7", UserAgent: "scanner/1.0",
	}}); err != nil {
		t.Fatal(err)
	}
	if boxed := read("?outcome=boxed"); len(boxed.Events) != 1 || boxed.Events[0].ClientIP != "203.0.113.7" || boxed.Events[0].Path != "/xmlrpc.php" {
		t.Fatalf("boxed = %+v", boxed.Events)
	}
	for _, bad := range []string{"?since=-1h", "?since=soon", "?limit=500", "?outcome=denied", "?rule=nope", "?cursor=x", "?request_id=" + strings.Repeat("r", 129)} {
		if rec := e.Do(t, "GET", "/v1/apps/"+slug+"/edge-rules/events"+bad, nil, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, rec.Code)
		}
	}
}
