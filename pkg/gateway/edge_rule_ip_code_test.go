package gateway

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// A kind=ip deny answered with code insufficient_scope, the API-key scope
// code, so a customer's runbook could not tell it from an auth failure
// (Hunt 12 H12-39).
func TestEdgeRuleIPDenyUsesIPDeniedCode(t *testing.T) {
	_, office, _ := net.ParseCIDR("198.51.100.0/24")
	_, blocked, _ := net.ParseCIDR("203.0.113.0/24")
	tests := []struct {
		name string
		rule EdgeRuleIPResolved
	}{
		{"deny CIDR", EdgeRuleIPResolved{ID: "rule-ip", AccountID: "acct-1", AppID: "app-1", Deny: []*net.IPNet{blocked}}},
		{"outside the allowlist", EdgeRuleIPResolved{ID: "rule-ip", AccountID: "acct-1", AppID: "app-1", Allow: []*net.IPNet{office}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &fakeBackend{
				app:      App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
				host:     "ip.example.com",
				upstream: "127.0.0.1:0",
				running:  true,
			}
			b.targets = append(b.targets, Target{NodeID: b.upstream, InstanceID: "i-fake"})
			h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
			h.SetWakeGateHook()
			rule := tt.rule
			h.WithEdgeRules(stubEdgeRuleMatcher{ip: &rule}, nil, nil)
			req := httptest.NewRequest(http.MethodGet, "http://ip.example.com/", nil)
			req.Header.Set("X-Forwarded-For", "203.0.113.9")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decode %q: %v", rec.Body.String(), err)
			}
			if rec.Code != http.StatusForbidden || problem.Code != api.CodeIPDenied {
				t.Fatalf("status %d code %q, want 403 %q", rec.Code, problem.Code, api.CodeIPDenied)
			}
		})
	}
}
