package gateway

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// adr: 091 — amendment 2026-10-28 (review round 3): kind=headers rules run
// before the IP-based gates. A rule that removed, added or rewrote
// X-Forwarded-For changed the address kind=ip judged (removing it refused
// every client as caller_ip_forged; a templated value let the client pick
// its address) and the one stamped for the app. The gateway now pins the
// address the request arrived with before any rule runs.
func TestHeaderRulesCannotMoveTheTrustedClientIP(t *testing.T) {
	_, denyNet, _ := net.ParseCIDR("203.0.113.0/24")
	for _, tc := range []struct {
		name     string
		op       EdgeRuleHeaderOp
		client   string
		wantCode int
	}{
		{name: "removing the header keeps an allowed client", op: EdgeRuleHeaderOp{Action: "remove", Name: "X-Forwarded-For"},
			client: "198.51.100.7", wantCode: http.StatusOK},
		{name: "adding a second value keeps an allowed client", op: EdgeRuleHeaderOp{Action: "add", Name: "X-Forwarded-For", Value: "192.0.2.1"},
			client: "198.51.100.7", wantCode: http.StatusOK},
		{name: "rewriting the header does not let a denied client through", op: EdgeRuleHeaderOp{Action: "set", Name: "X-Forwarded-For", Value: "198.51.100.9"},
			client: "203.0.113.7", wantCode: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &fakeBackend{app: App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}, host: "p.example.com", upstream: "127.0.0.1:0", running: true}
			b.targets = append(b.targets, Target{NodeID: b.upstream, InstanceID: "i-fake"})
			h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
			h.SetWakeGateHook()
			h.WithEdgeRules(stubEdgeRuleMatcher{
				headers: &EdgeRuleHeadersResolved{ID: "rule-headers", AccountID: "acct-1", AppID: "app-1", RequestHeaders: []EdgeRuleHeaderOp{tc.op}},
				ip:      &EdgeRuleIPResolved{ID: "rule-deny", AccountID: "acct-1", AppID: "app-1", Deny: []*net.IPNet{denyNet}},
			}, nil, nil)
			var stamped string
			h.WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					stamped = r.Header.Get(wire.ClientIPHeader)
					w.WriteHeader(http.StatusOK)
				})
			})
			req := httptest.NewRequest(http.MethodGet, "http://p.example.com/admin", nil)
			req.Header.Set("X-Forwarded-For", tc.client)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body, tc.wantCode)
			}
			if tc.wantCode == http.StatusOK && stamped != tc.client {
				t.Fatalf("app saw client %q, want %q", stamped, tc.client)
			}
		})
	}
}
