package gateway

// adr: 20261022105846 — amendment 2: a client put in a penalty box is a
// sampled security event, recorded once, from the request that tipped it
// in, on its own budget; the box holds whether or not the event is kept.

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type penaltyEventsRecorder struct {
	events     []EdgeRuleEvent
	sampled    []string
	allowBoxes bool
}

func (f *penaltyEventsRecorder) RecordEdgeRuleHit(string, string, bool) {}
func (f *penaltyEventsRecorder) SampleEdgePenaltyBox(ruleID string) bool {
	f.sampled = append(f.sampled, ruleID)
	return f.allowBoxes
}
func (f *penaltyEventsRecorder) RecordEdgeRuleEvent(ev EdgeRuleEvent) {
	f.events = append(f.events, ev)
}

// hitsOnlyRecorder counts hits but keeps no penalty events.
type hitsOnlyRecorder struct{}

func (hitsOnlyRecorder) RecordEdgeRuleHit(string, string, bool) {}

func TestPenaltyBoxRecordsBoxedEvent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		recorder EdgeRuleHitRecorder
		want     int
	}{
		{name: "recorded", recorder: &penaltyEventsRecorder{allowBoxes: true}, want: 1},
		{name: "over the budget", recorder: &penaltyEventsRecorder{}},
		{name: "recorder keeps no penalty events", recorder: hitsOnlyRecorder{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &fakeBackend{
				app:      App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
				host:     "p.example.com",
				upstream: "127.0.0.1:0",
				running:  true,
			}
			b.targets = append(b.targets, Target{NodeID: b.upstream, InstanceID: "i-fake"})
			h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
			h.SetWakeGateHook()
			_, denyNet, _ := net.ParseCIDR("203.0.113.0/24")
			h.WithEdgeRules(penaltyMatcher{
				stubEdgeRuleMatcher: stubEdgeRuleMatcher{ip: &EdgeRuleIPResolved{
					ID: "rule-scanners", AccountID: "acct-1", AppID: "app-1", Deny: []*net.IPNet{denyNet},
				}},
				penalties: map[string]api.EdgeRulePenalty{"rule-scanners": {Seconds: 600, After: 2, WithinSeconds: 60}},
			}, nil, nil)
			h.WithPenaltyBox(NewPenaltyBox())
			h.WithEdgeRuleHitRecorder(tc.recorder)

			get := func(path, requestID string) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(http.MethodGet, "http://p.example.com"+path, nil)
				req.Header.Set("X-Forwarded-For", "203.0.113.7")
				req.Header.Set("User-Agent", "scanner/1.0")
				req.Header.Set(api.RequestIDHeader, requestID)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				return rec
			}
			get("/wp-login.php", "req-1")
			get("/xmlrpc.php", "req-2") // the second refusal boxes the client
			for _, id := range []string{"req-3", "req-4"} {
				if rec := get("/", id); !strings.Contains(rec.Body.String(), api.CodePenaltyBox) {
					t.Fatalf("%s: client not boxed: %d %s", id, rec.Code, rec.Body)
				}
			}

			f, ok := tc.recorder.(*penaltyEventsRecorder)
			if !ok {
				return
			}
			if len(f.sampled) != 1 || f.sampled[0] != "rule-scanners" {
				t.Fatalf("budget asked %v, want once for rule-scanners", f.sampled)
			}
			var boxed []EdgeRuleEvent
			for _, ev := range f.events {
				if ev.Outcome == EdgeRuleEventBoxed {
					boxed = append(boxed, ev)
				}
			}
			if len(boxed) != tc.want {
				t.Fatalf("boxed events = %+v, want %d", boxed, tc.want)
			}
			if tc.want == 0 {
				return
			}
			ev := boxed[0]
			if ev.RuleID != "rule-scanners" || ev.AppID != "app-1" || ev.ClientIP != "203.0.113.7" || ev.RequestID != "req-2" ||
				ev.Path != "/xmlrpc.php" || ev.Method != http.MethodGet || ev.Host != "p.example.com" || ev.UserAgent != "scanner/1.0" || ev.At.IsZero() {
				t.Fatalf("boxed event = %+v, want the request that tipped the client in", ev)
			}
		})
	}
}
