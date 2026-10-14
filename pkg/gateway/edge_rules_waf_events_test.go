package gateway

// adr: 831 — amendment 5: kind=waf detections become sampled security events.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// wafEventsRecorder records hits, sampled match events and detection events.
type wafEventsRecorder struct {
	hits            []string
	events          []EdgeRuleEvent
	allowDetections bool
}

func (f *wafEventsRecorder) RecordEdgeRuleHit(ruleID, _ string, _ bool) {
	f.hits = append(f.hits, ruleID)
}
func (f *wafEventsRecorder) SampleEdgeRuleEvent(string) bool      { return true }
func (f *wafEventsRecorder) SampleEdgeRuleDetection(string) bool  { return f.allowDetections }
func (f *wafEventsRecorder) RecordEdgeRuleEvent(ev EdgeRuleEvent) { f.events = append(f.events, ev) }

func TestNewWAFDetectionEvent(t *testing.T) {
	header := http.Header{}
	header.Set("User-Agent", "sqlmap/1.7.2")
	ids := make([]int, EdgeRuleEventMaxWAFRuleIDs+5)
	for i := range ids {
		ids[i] = 942100 + i
	}
	s := WAFSample{
		AppID: "app-1", RuleID: "rule-1", RequestID: "req-1", Method: http.MethodGet,
		Host: "api.example.com:8443", URI: "/login?user=admin&token=secret", ClientIP: "203.0.113.9", Header: header,
		country: func() string { return "DE" },
	}
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.FixedZone("x", 3600))
	ev := NewWAFDetectionEvent(s, EdgeRuleEventBlocked, WAFDetection{RuleIDs: ids, Categories: []string{"sqli"}, Score: 99999}, at)
	if ev.Path != "/login" || strings.Contains(ev.Path, "secret") {
		t.Errorf("path = %q, want no query string", ev.Path)
	}
	if ev.Host != "api.example.com" || ev.Country != "DE" || ev.UserAgent != "sqlmap/1.7.2" || ev.RequestID != "req-1" || ev.ClientIP != "203.0.113.9" {
		t.Errorf("event request fields = %+v", ev)
	}
	if ev.Outcome != EdgeRuleEventBlocked || ev.RuleID != "rule-1" || ev.AppID != "app-1" || !ev.At.Equal(at) || ev.At.Location() != time.UTC {
		t.Errorf("event identity = %+v", ev)
	}
	if len(ev.WAFRuleIDs) != EdgeRuleEventMaxWAFRuleIDs || ev.WAFScore != EdgeRuleEventMaxWAFScore || ev.WAFCategories[0] != "sqli" {
		t.Errorf("WAF detail not bounded: %d rules, score %d", len(ev.WAFRuleIDs), ev.WAFScore)
	}
	ids[0] = 1
	if ev.WAFRuleIDs[0] != 942100 {
		t.Error("event aliases the caller's rule ID slice")
	}
	if got := NewWAFDetectionEvent(WAFSample{URI: "/"}, EdgeRuleEventDetected, WAFDetection{}, at); got.Country != "" || got.UserAgent != "" {
		t.Errorf("sample without context or headers = %+v", got)
	}
}

// Every inspected request matches a WAF rule, so WAF matches are counted but
// never sampled as events; other kinds still are.
func TestObserveEdgeRuleMatchDoesNotSampleWAFMatches(t *testing.T) {
	rec := &wafEventsRecorder{}
	r := httptest.NewRequest(http.MethodGet, "http://api.example.com/x", nil)
	ctx := WithEdgeRuleMatchContext(context.Background(), NewEdgeRuleMatchContext(r, nil, nil, rec))

	ObserveEdgeRuleMatch(ctx, &EdgeRuleWAFResolved{EdgeRuleCondition: EdgeRuleCondition{RuleID: "waf", AppID: "app"}}, nil)
	ObserveEdgeRuleMatch(ctx, nil, &EdgeRuleWAFResolved{EdgeRuleCondition: EdgeRuleCondition{RuleID: "waf-log", AppID: "app", LogOnly: true}})
	if len(rec.hits) != 2 || len(rec.events) != 0 {
		t.Fatalf("WAF matches: hits %v, events %d; want 2 hits and no events", rec.hits, len(rec.events))
	}
	ObserveEdgeRuleMatch(ctx, &EdgeRuleAsyncResolved{EdgeRuleCondition: EdgeRuleCondition{RuleID: "async", AppID: "app"}}, nil)
	if len(rec.events) != 1 || rec.events[0].RuleID != "async" {
		t.Fatalf("other kinds' matches are no longer sampled: %+v", rec.events)
	}
}

func TestApplyEdgeRuleWAFInlineRecordsDetectionEvents(t *testing.T) {
	detected := WAFInlineResult{Outcome: WAFInlineDetected, RuleIDs: []int{913100}, Categories: []string{"scanner"}, Score: 5, Seconds: 0.001}
	for _, tc := range []struct {
		name    string
		mode    string
		verdict WAFInlineResult
		allow   bool
		want    string
	}{
		{name: "block", mode: "block", verdict: detected, allow: true, want: EdgeRuleEventBlocked},
		{name: "warn", mode: "warn", verdict: detected, allow: true, want: EdgeRuleEventWarned},
		{name: "over the detection budget", mode: "block", verdict: detected, allow: false},
		{name: "clean", mode: "block", verdict: WAFInlineResult{Outcome: WAFInlineClean}, allow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &wafEventsRecorder{allowDetections: tc.allow}
			rule := &EdgeRuleWAFResolved{ID: "rule-1", AccountID: "acct-1", AppID: "app-1", ParanoiaLevel: 1, AnomalyThreshold: 5, Mode: tc.mode}
			h := &Handler{edgeRules: wafRuleMatcher{rule: rule}, wafInline: &fakeWAFInline{result: tc.verdict}, edgeRuleHits: rec}
			r := httptest.NewRequest(http.MethodGet, "http://app.example.test/admin/?debug=1", nil)
			r.Header.Set("User-Agent", "sqlmap/1.7.2")
			w := httptest.NewRecorder()
			sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: r}
			h.applyEdgeRuleWAF(sr, r, App{ID: "app-1", AccountID: "acct-1"}, sr)
			if tc.want == "" {
				if len(rec.events) != 0 {
					t.Fatalf("events = %+v, want none", rec.events)
				}
				return
			}
			if len(rec.events) != 1 {
				t.Fatalf("events = %d, want 1", len(rec.events))
			}
			ev := rec.events[0]
			if ev.Outcome != tc.want || ev.RuleID != "rule-1" || ev.Path != "/admin/" || ev.WAFScore != 5 ||
				len(ev.WAFRuleIDs) != 1 || ev.WAFRuleIDs[0] != 913100 || ev.WAFCategories[0] != "scanner" || ev.UserAgent != "sqlmap/1.7.2" {
				t.Errorf("event = %+v", ev)
			}
		})
	}
}
