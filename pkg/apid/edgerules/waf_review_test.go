package edgerules

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 831 — amendment 13: id-like path segments group together; other
// segments, file names included, are kept.
func TestReviewPath(t *testing.T) {
	for in, want := range map[string]string{
		"/orders/17/items": "/orders/{id}/items",
		"/users/3f2c1a9e-5b7d-4c11-9e2a-0d6b8f4a1c22": "/users/{id}",
		"/blobs/deadbeefcafe0123":                     "/blobs/{id}",
		"/s/AbC123xyz789QwErTy00":                     "/s/{id}",
		"/search":                                     "/search",
		"/static/app.v2.js":                           "/static/app.v2.js",
		"/v1/health-check":                            "/v1/health-check",
		"/":                                           "/",
		"/decade":                                     "/decade",
	} {
		if got := reviewPath(in); got != want {
			t.Errorf("reviewPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func wafEvent(id int64, at time.Time, outcome, path, ip string, crs ...int) state.EdgeRuleEvent {
	return state.EdgeRuleEvent{ID: id, RuleID: "r1", OccurredAt: at, Outcome: outcome, Path: path, ClientIP: ip,
		RequestID: fmt.Sprintf("req-%d", id), WAFRuleIDs: crs}
}

// adr: 831 — amendment 13: detections group by edge rule, CRS rule and
// path; many unrelated clients on one path, a documented false positive, a
// scanner and an excluded rule are each assessed, and examples are newest
// first.
func TestWAFReviewGroupsAndAssesses(t *testing.T) {
	t0 := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	rules := []state.EdgeRule{{ID: "r1", Name: "waf", Kind: state.EdgeRuleKindWAF, Action: state.EdgeRuleAction{
		Kind: state.EdgeRuleKindWAF, WAF: &state.EdgeRuleWAFAction{Mode: api.EdgeWAFModeBlock, ExcludeRuleIDs: []int{920350}}}}}
	var events []state.EdgeRuleEvent
	id := int64(0)
	add := func(at time.Duration, outcome, path, ip string, crs ...int) {
		id++
		events = append(events, wafEvent(id, t0.Add(at), outcome, path, ip, crs...))
	}
	for i, ip := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3", "198.51.100.4"} {
		add(time.Duration(i)*time.Minute, state.EdgeRuleEventBlocked, "/search", ip, 942360)
	}
	add(5*time.Minute, state.EdgeRuleEventWarned, "/search", "198.51.100.1", 942360)
	for i := range 6 {
		add(time.Duration(10+i)*time.Minute, state.EdgeRuleEventDetected, fmt.Sprintf("/probe-%c", 'a'+i), "203.0.113.9", 930120, 930120)
	}
	add(20*time.Minute, state.EdgeRuleEventBlocked, "/graphql", "192.0.2.7", 942190)
	add(21*time.Minute, state.EdgeRuleEventDetected, "/upload", "192.0.2.8", 920350)
	add(22*time.Minute, state.EdgeRuleEventDetected, "/comments/17", "192.0.2.9", 941100)
	add(23*time.Minute, state.EdgeRuleEventDetected, "/comments/18", "192.0.2.9", 941100)
	slices.Reverse(events)

	resp := WAFReview(t0.Add(-time.Hour), events, rules, true)
	if resp.Scanned != len(events) || !resp.Truncated {
		t.Fatalf("scanned=%d truncated=%v, want %d true", resp.Scanned, resp.Truncated, len(events))
	}
	byKey := map[string]api.EdgeWAFReviewGroup{}
	for _, g := range resp.Groups {
		byKey[fmt.Sprintf("%d %s", g.CRSRuleID, g.Path)] = g
	}
	if len(byKey) != 10 {
		t.Fatalf("groups = %d (%v), want 10: one per probe path plus four", len(byKey), resp.Groups)
	}

	search := byKey["942360 /search"]
	if search.Assessment != api.EdgeWAFReviewLikelyFalsePositive || search.Clients != 4 || search.Events != 5 ||
		search.Blocked != 4 || search.Warned != 1 || search.TopClientPercent != 40 || search.Category != "sqli" || search.RuleMode != "block" {
		t.Errorf("search group = %+v", search)
	}
	if !slices.Equal(search.RequestIDs, []string{"req-5", "req-4", "req-3"}) || !search.LastSeen.Equal(t0.Add(5*time.Minute)) || !search.FirstSeen.Equal(t0) {
		t.Errorf("search examples %v, seen %s..%s", search.RequestIDs, search.FirstSeen, search.LastSeen)
	}
	if probe := byKey["930120 /probe-a"]; probe.Assessment != api.EdgeWAFReviewLikelyAttack || probe.ScannerPercent != 100 || probe.Events != 1 {
		t.Errorf("probe group = %+v, want a likely attack counted once per event", probe)
	}
	if gql := byKey["942190 /graphql"]; gql.Assessment != api.EdgeWAFReviewLikelyFalsePositive || gql.KnownFalsePositive == "" {
		t.Errorf("graphql group = %+v, want the documented false positive", gql)
	}
	if up := byKey["920350 /upload"]; up.Assessment != api.EdgeWAFReviewExcluded || !up.Excluded {
		t.Errorf("upload group = %+v, want excluded", up)
	}
	if c := byKey["941100 /comments/{id}"]; c.Assessment != api.EdgeWAFReviewUndecided || c.Events != 2 || c.Clients != 1 {
		t.Errorf("comments group = %+v, want one undecided group for both ids", c)
	}
	if resp.Groups[0].CRSRuleID != 942360 {
		t.Errorf("first group = %d, want the largest (942360)", resp.Groups[0].CRSRuleID)
	}
}

// adr: 831 — amendment 13: a review returns at most EdgeWAFReviewMaxGroups
// groups.
func TestWAFReviewCapsGroups(t *testing.T) {
	t0 := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	var events []state.EdgeRuleEvent
	for i := range api.EdgeWAFReviewMaxGroups + 5 {
		events = append(events, wafEvent(int64(i+1), t0, state.EdgeRuleEventDetected, fmt.Sprintf("/p%c%c", 'a'+i/26, 'a'+i%26), "192.0.2.1", 942100))
	}
	if got := len(WAFReview(t0, events, nil, false).Groups); got != api.EdgeWAFReviewMaxGroups {
		t.Fatalf("groups = %d, want %d", got, api.EdgeWAFReviewMaxGroups)
	}
}

// adr: 831 — amendment 13: an exclusion update adds and removes CRS rule IDs
// and keeps the rest of the waf action; other kinds and empty, overlapping
// or out-of-range requests are refused.
func TestWAFExclusionsUpdate(t *testing.T) {
	waf := state.EdgeRule{ID: "r1", Kind: state.EdgeRuleKindWAF, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindWAF,
		WAF: &state.EdgeRuleWAFAction{Mode: api.EdgeWAFModeBlock, ParanoiaLevel: 1, AnomalyThreshold: 10, InspectBodyBytes: 4096, ExcludeRuleIDs: []int{920350, 942100}}}}
	update, prob := WAFExclusionsUpdate(waf, api.EdgeWAFExclusionsRequest{Add: []int{942190, 942100}, Remove: []int{920350}})
	if prob != nil {
		t.Fatalf("refused: %s", prob.Detail)
	}
	var got api.EdgeRuleWAFAction
	if update.Action == nil || json.Unmarshal(*update.Action, &got) != nil {
		t.Fatalf("update action = %v", update.Action)
	}
	want := api.EdgeRuleWAFAction{Mode: api.EdgeWAFModeBlock, ParanoiaLevel: 1, AnomalyThreshold: 10, InspectBodyBytes: 4096, ExcludeRuleIDs: []int{942100, 942190}}
	if got.Mode != want.Mode || got.AnomalyThreshold != want.AnomalyThreshold || got.InspectBodyBytes != want.InspectBodyBytes || !slices.Equal(got.ExcludeRuleIDs, want.ExcludeRuleIDs) {
		t.Fatalf("action = %+v, want %+v", got, want)
	}
	if update.Penalty != nil || update.ClearPenalty {
		t.Fatalf("update touches the penalty: %+v", update)
	}

	tooMany := make([]int, api.MaxEdgeWAFExcludedRules)
	for i := range tooMany {
		tooMany[i] = 941000 + i
	}
	ip := state.EdgeRule{ID: "r2", Kind: state.EdgeRuleKindIP, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{}}}
	for name, tc := range map[string]struct {
		row state.EdgeRule
		req api.EdgeWAFExclusionsRequest
	}{
		"not waf":      {ip, api.EdgeWAFExclusionsRequest{Add: []int{942100}}},
		"empty":        {waf, api.EdgeWAFExclusionsRequest{}},
		"overlap":      {waf, api.EdgeWAFExclusionsRequest{Add: []int{942190}, Remove: []int{942190}}},
		"out of range": {waf, api.EdgeWAFExclusionsRequest{Add: []int{12}}},
		"over the cap": {waf, api.EdgeWAFExclusionsRequest{Add: tooMany}},
	} {
		if _, prob := WAFExclusionsUpdate(tc.row, tc.req); prob == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// adr: 831 — amendment 13: the review window defaults to 7 days, capped by
// the plan's event window, and reads one event past the bound to detect
// truncation.
func TestWAFReviewQuery(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		plan  api.Plan
		query string
		want  time.Duration
	}{
		{api.PlanPro, "", 7 * 24 * time.Hour},
		{api.PlanFree, "", 24 * time.Hour},
		{api.PlanPro, "?since=2h", 2 * time.Hour},
	} {
		q, prob := WAFReviewQuery(httptest.NewRequest("GET", "/x"+tc.query, nil), tc.plan, "app", now)
		if prob != nil || now.Sub(q.Since) != tc.want || q.Limit != api.EdgeWAFReviewMaxEvents+1 {
			t.Errorf("%s %q: since %s limit %d prob %v, want %s", tc.plan, tc.query, now.Sub(q.Since), q.Limit, prob, tc.want)
		}
	}
	if _, prob := WAFReviewQuery(httptest.NewRequest("GET", "/x?rule=nope", nil), api.PlanPro, "app", now); prob == nil {
		t.Error("a malformed rule id was accepted")
	}
}

type wafReviewEventStore struct{ perOutcome int }

func (s wafReviewEventStore) RecordEdgeRuleEvents(context.Context, []state.EdgeRuleEvent) error {
	return nil
}

func (s wafReviewEventStore) PruneEdgeRuleEvents(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (s wafReviewEventStore) ListEdgeRuleEvents(_ context.Context, q state.EdgeRuleEventQuery) ([]state.EdgeRuleEvent, error) {
	out := make([]state.EdgeRuleEvent, min(s.perOutcome, q.Limit))
	for i := range out {
		out[i] = state.EdgeRuleEvent{Outcome: q.Outcome}
	}
	return out, nil
}

// adr: 831 — amendment 13: each detection outcome is read up to the bound,
// and a review over the bound says it was truncated.
func TestWAFReviewEventsTruncates(t *testing.T) {
	q := state.EdgeRuleEventQuery{AppID: "app", Limit: api.EdgeWAFReviewMaxEvents + 1}
	events, truncated, err := wafReviewEvents(context.Background(), wafReviewEventStore{perOutcome: 10}, q)
	if err != nil || truncated || len(events) != 30 {
		t.Fatalf("under the bound: %d events truncated=%v err=%v", len(events), truncated, err)
	}
	events, truncated, err = wafReviewEvents(context.Background(), wafReviewEventStore{perOutcome: api.EdgeWAFReviewMaxEvents + 50}, q)
	if err != nil || !truncated || len(events) != 3*api.EdgeWAFReviewMaxEvents {
		t.Fatalf("over the bound: %d events truncated=%v err=%v", len(events), truncated, err)
	}
}
