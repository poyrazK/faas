// adr: 091
package gateway

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 091 — constraining gates (jwt, ip, geo, limit, throttle, validate,
// maintenance) must not be bypassed by a path the app's router treats as the
// same resource: dot segments, doubled slashes, or a different letter case.
// Non-protective kinds keep exact matching so they never widen.
func TestProtectivePathMatchCoversNormalizedAndCaseVariants(t *testing.T) {
	for _, tc := range []struct {
		glob, path string
		want       bool
	}{
		{"/admin/*", "/admin/users", true},
		{"/admin/*", "/ADMIN/users", true},
		{"/Admin/*", "/admin/users", true},
		{"/admin/*", "/public/../admin/users", true},
		{"/admin/*", "//admin/users", true},
		{"/admin/*", "/Public/../ADMIN/x", true},
		{"/admin/*", "/administrator/x", false},
		{"/admin/*", "/public/x", false},
	} {
		got, err := protectivePathMatch(tc.glob, tc.path)
		if err != nil || got != tc.want {
			t.Errorf("protectivePathMatch(%q, %q) = %v, %v; want %v", tc.glob, tc.path, got, err, tc.want)
		}
	}
	if ok, _ := pathGlobMatch("/admin/*", "/ADMIN/users"); ok {
		t.Fatal("non-protective matching widened to a case variant")
	}
	limit := []EdgeRuleLimitResolved{{ID: "cap", PathGlob: "/upload/*", MaxBodyBytes: 1}}
	if got := PickFirstLimitMatch(limit, "/UPLOAD/big", http.MethodPost); got == nil {
		t.Fatal("kind=limit body cap bypassed by path case")
	}
	maintenance := []EdgeRuleMaintenanceResolved{{ID: "m", PathGlob: "/billing/*"}}
	if got := PickFirstMaintenanceMatch(maintenance, "/x/../billing/pay", http.MethodGet); got == nil {
		t.Fatal("kind=maintenance bypassed by a dot-segment path")
	}
}

// adr: 091 — D4: kind=ip is the cheap deny before auth. A request from a
// denied address is rejected without verifying (or fetching keys for) its
// bearer token.
func TestEdgeRuleIPDeniesBeforeJWTVerification(t *testing.T) {
	b := &fakeBackend{
		app:      App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
		host:     "j.example.com",
		upstream: "127.0.0.1:0",
		running:  true,
	}
	b.targets = append(b.targets, Target{NodeID: b.upstream, InstanceID: "i-fake"})
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.SetWakeGateHook()
	_, denied, _ := net.ParseCIDR("203.0.113.0/24")
	h.WithEdgeRules(stubEdgeRuleMatcher{
		jwt: &EdgeRuleJWTResolved{
			ID: "rule-jwt", AccountID: "acct-1", AppID: "app-1",
			Issuer: "https://idp.example.com", JWKSURL: "https://idp.example.com/jwks", Algorithms: []string{"RS256"},
		},
		ip: &EdgeRuleIPResolved{ID: "rule-ip", AccountID: "acct-1", AppID: "app-1", Deny: []*net.IPNet{denied}},
	}, nil, nil)
	var verifyCalls atomic.Int32
	h.WithJWTVerifier(&countingJWTVerifier{onVerify: func(context.Context, string, *EdgeRuleJWTResolved) (*JWTClaims, error) {
		verifyCalls.Add(1)
		return &JWTClaims{Subject: "alice"}, nil
	}})

	req := httptest.NewRequest(http.MethodGet, "http://j.example.com/", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 from the IP gate (%s)", rec.Code, rec.Body.String())
	}
	if got := verifyCalls.Load(); got != 0 {
		t.Fatalf("JWT verifier calls = %d, want 0 for an IP-denied request", got)
	}
}

// adr: 122 — a response the origin varies on a header the cache key ignores
// must not be stored, or one client's variant (e.g. its CORS grant) is
// replayed to everyone.
func TestCacheWriterHonoursOriginVary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		varyOn   []string
		vary     []string
		encoding string
		want     bool
	}{
		{"no vary", nil, nil, "", true},
		{"vary covered by key", []string{"Accept-Language"}, []string{"accept-language"}, "", true},
		{"vary origin not keyed", nil, []string{"Origin"}, "", false},
		{"one of several unkeyed", []string{"Accept-Language"}, []string{"Accept-Language, Authorization"}, "", false},
		{"vary star", []string{"Accept-Language"}, []string{"*"}, "", false},
		{"accept-encoding on identity body", nil, []string{"Accept-Encoding"}, "", true},
		{"accept-encoding on gzip body", nil, []string{"Accept-Encoding"}, "gzip", false},
		{"accept-encoding keyed on gzip body", []string{"Accept-Encoding"}, []string{"Accept-Encoding"}, "gzip", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &cacheWriter{
				rule:      &EdgeRuleCacheResolved{VaryOn: tc.varyOn},
				header:    http.Header{},
				status:    http.StatusOK,
				wroteBody: true,
			}
			for _, v := range tc.vary {
				c.header.Add("Vary", v)
			}
			if tc.encoding != "" {
				c.header.Set("Content-Encoding", tc.encoding)
			}
			if got := c.shouldStore(); got != tc.want {
				t.Fatalf("shouldStore = %v, want %v", got, tc.want)
			}
		})
	}
}
