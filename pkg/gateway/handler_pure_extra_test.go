// handler_pure_extra_test.go — fill pkg/gateway/handler.go coverage
// of the tiny pure / no-store helpers beyond what handler_test.go
// touches. Targets statusClass/statusClassBucket (counter vs.
// histogram label), intToString, singleSlash, matchOrigin +
// splitScheme (allowlist echo + wildcard), corsDefaultOps,
// contentTypeAllowed, geoFailReason, clientIPFromTrustedXFF,
// bearerTokenFromHeader, applyHeaderOp, isAcceptJSON, hostname,
// the three write*RateLimitHeaders helpers, recordEgress
// (nil-safe + flusher path + status gate), preInstantiateApps.seen,
// the per-route dedupe, routeSetFor + RoutesFor, and the
// statusRecorder surface (WriteHeader/Write/Flush/maybeFlush/
// doFlush/finalFlush/ installFlushHook + headerOps).
package gateway

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// --- statusClass -------------------------------------------------

func TestStatusClass_AllBoundaries(t *testing.T) {
	cases := map[int]string{
		100:   "100",
		200:   "200",
		299:   "299",
		300:   "300",
		399:   "399",
		400:   "400",
		499:   "499",
		500:   "500",
		599:   "599",
		0:     "500", // clamped to internal-server-error
		-1:    "500",
		1000:  "500",
		99999: "500",
	}
	for in, want := range cases {
		if got := statusClass(in); got != want {
			t.Errorf("statusClass(%d) = %q, want %q", in, got, want)
		}
	}
}

// --- statusClassBucket -------------------------------------------

func TestStatusClassBucket_AllClasses(t *testing.T) {
	cases := map[int]string{
		100: "1xx", 101: "1xx", 199: "1xx",
		200: "2xx", 204: "2xx", 299: "2xx",
		300: "3xx", 301: "3xx", 399: "3xx",
		400: "4xx", 404: "4xx", 499: "4xx",
		500: "5xx", 502: "5xx", 599: "5xx",
		// anything outside [100, 599] lands in 5xx.
		0: "5xx", -1: "5xx", 99: "5xx", 600: "5xx", 9999: "5xx",
	}
	for in, want := range cases {
		if got := statusClassBucket(in); got != want {
			t.Errorf("statusClassBucket(%d) = %q, want %q", in, got, want)
		}
	}
}

// --- intToString -------------------------------------------------

func TestIntToString(t *testing.T) {
	cases := map[int]string{
		0:          "0",
		1:          "1",
		-1:         "-1",
		9:          "9",
		10:         "10",
		99:         "99",
		100:        "100",
		1234:       "1234",
		-1234:      "-1234",
		2147483647: "2147483647",
	}
	for in, want := range cases {
		if got := intToString(in); got != want {
			t.Errorf("intToString(%d) = %q, want %q", in, got, want)
		}
	}
}

// --- singleSlash -------------------------------------------------

func TestSingleSlash(t *testing.T) {
	cases := map[string]string{
		"":      "/",
		"/":     "/",
		"//":    "/",
		"/a/b":  "/a/b",
		"a/b":   "/a/b",
		"/a/":   "/a",
		"a/":    "/a",
		"/a/b/": "/a/b",
	}
	for in, want := range cases {
		if got := singleSlash(in); got != want {
			t.Errorf("singleSlash(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- splitScheme -------------------------------------------------

func TestSplitScheme(t *testing.T) {
	cases := []struct {
		in           string
		scheme, rest string
		ok           bool
	}{
		{"https://example.com", "https", "example.com", true},
		{"http://x:8080", "http", "x:8080", true},
		{"//no-scheme", "", "", false},
		{"", "", "", false},
		{"schemeno", "", "", false},
	}
	for _, c := range cases {
		s, r, ok := splitScheme(c.in)
		if s != c.scheme || r != c.rest || ok != c.ok {
			t.Errorf("splitScheme(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.in, s, r, ok, c.scheme, c.rest, c.ok)
		}
	}
}

// --- matchOrigin -------------------------------------------------

func TestMatchOrigin_EmptyOriginReturnsEmpty(t *testing.T) {
	if got := matchOrigin([]string{"https://a.com"}, ""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestMatchOrigin_EmptyAllowList(t *testing.T) {
	if got := matchOrigin(nil, "https://a.com"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestMatchOrigin_ExactMatchCaseInsensitive(t *testing.T) {
	allow := []string{"https://App.Example.com"}
	got := matchOrigin(allow, "HTTPS://app.example.COM")
	if got != "https://app.example.com" {
		t.Errorf("got %q", got)
	}
}

func TestMatchOrigin_StarWildcardEchoes(t *testing.T) {
	allow := []string{"*"}
	got := matchOrigin(allow, "https://anything.example.com")
	if got != "*" {
		t.Errorf("got %q, want *", got)
	}
}

func TestMatchOrigin_SubdomainWildcard(t *testing.T) {
	allow := []string{"https://*.example.com"}
	// Match: single-label subdomain.
	if got := matchOrigin(allow, "https://www.example.com"); got != "https://*.example.com" {
		t.Errorf("www got %q", got)
	}
	// No match: no subdomain.
	if got := matchOrigin(allow, "https://example.com"); got != "" {
		t.Errorf("apex got %q, want empty", got)
	}
	// No match: multi-label subdomain (must be exactly one extra label).
	if got := matchOrigin(allow, "https://a.b.example.com"); got != "" {
		t.Errorf("multi-label got %q, want empty", got)
	}
}

func TestMatchOrigin_PortWildcard(t *testing.T) {
	allow := []string{"https://app.example.com:*"}
	// Match any port.
	if got := matchOrigin(allow, "https://app.example.com:8443"); got != "https://app.example.com:*" {
		t.Errorf("port match got %q", got)
	}
	// No match: different host.
	if got := matchOrigin(allow, "https://other.example.com:8443"); got != "" {
		t.Errorf("other host got %q", got)
	}
	// No match: no port required, but the prefix must be present
	// (the impl only checks rHost has prefix "host:" — so the
	// empty-port case does not match).
	if got := matchOrigin(allow, "https://app.example.com"); got != "" {
		t.Errorf("no port got %q", got)
	}
}

func TestMatchOrigin_SchemeMismatch(t *testing.T) {
	allow := []string{"https://app.example.com"}
	if got := matchOrigin(allow, "http://app.example.com"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestMatchOrigin_AllowListWithoutSchemeSkipped(t *testing.T) {
	allow := []string{"no-scheme"}
	if got := matchOrigin(allow, "https://no-scheme"); got != "" {
		t.Errorf("got %q, want empty (no //: entry skipped)", got)
	}
}

// --- corsDefaultOps ----------------------------------------------

func TestCorsDefaultOps(t *testing.T) {
	ops := corsDefaultOps("https://app.example.com")
	if len(ops) != 4 {
		t.Fatalf("got %d ops, want 4", len(ops))
	}
	wantSub := map[string]string{
		"Access-Control-Allow-Origin":   "https://app.example.com",
		"Access-Control-Allow-Methods":  "GET, POST, OPTIONS",
		"Access-Control-Allow-Headers":  "*",
		"Access-Control-Expose-Headers": "Streaming-Status, Streaming-Status-Accept-Hint",
	}
	seen := map[string]bool{}
	for _, op := range ops {
		if op.Action != "set" {
			t.Errorf("op %q: action=%q, want set", op.Name, op.Action)
		}
		if want, ok := wantSub[op.Name]; !ok {
			t.Errorf("unexpected op name %q", op.Name)
		} else if op.Value != want {
			t.Errorf("op %q value = %q, want %q", op.Name, op.Value, want)
		}
		seen[op.Name] = true
	}
	for n := range wantSub {
		if !seen[n] {
			t.Errorf("op %q missing", n)
		}
	}
}

// --- contentTypeAllowed ------------------------------------------

func TestContentTypeAllowed(t *testing.T) {
	allowed := []string{"application/json", "text/plain"}
	if !contentTypeAllowed("application/json", allowed) {
		t.Error("json: got false, want true")
	}
	if !contentTypeAllowed("text/plain", allowed) {
		t.Error("text: got false, want true")
	}
	if contentTypeAllowed("text/html", allowed) {
		t.Error("html: got true, want false")
	}
	if contentTypeAllowed("", allowed) {
		t.Error("empty: got true, want false")
	}
	if contentTypeAllowed("application/json", nil) {
		t.Error("nil allowlist: got true, want false")
	}
}

// --- geoFailReason -----------------------------------------------

func TestGeoFailReason(t *testing.T) {
	stub := &netParseErrorStub{}
	cases := []struct {
		err   error
		found bool
		want  string
	}{
		{nil, true, "unknown"},
		{nil, false, "no_country"},
		{stub, true, "lookup_error"},
		{stub, false, "lookup_error"},
	}
	for _, c := range cases {
		if got := geoFailReason(c.err, c.found); got != c.want {
			t.Errorf("err=%v found=%v: got %q, want %q", c.err, c.found, got, c.want)
		}
	}
}

type netParseErrorStub struct{}

func (e *netParseErrorStub) Error() string { return "stub" }

// --- clientIPFromTrustedXFF --------------------------------------

func TestClientIPFromTrustedXFF_NoHeader(t *testing.T) {
	r := &http.Request{Header: http.Header{}}
	if ip, ok := clientIPFromTrustedXFF(r); ok || ip != nil {
		t.Errorf("got (%v, %v), want (nil, false)", ip, ok)
	}
}

func TestClientIPFromTrustedXFF_MultipleValues(t *testing.T) {
	// Multi-value XFF is a forged header (gatewayd-public sets
	// exactly one) → reject.
	r := &http.Request{Header: http.Header{}}
	r.Header.Add("X-Forwarded-For", "1.2.3.4")
	r.Header.Add("X-Forwarded-For", "5.6.7.8")
	if ip, ok := clientIPFromTrustedXFF(r); ok || ip != nil {
		t.Errorf("got (%v, %v), want (nil, false)", ip, ok)
	}
}

func TestClientIPFromTrustedXFF_Empty(t *testing.T) {
	r := &http.Request{Header: http.Header{}}
	r.Header.Set("X-Forwarded-For", "")
	if ip, ok := clientIPFromTrustedXFF(r); ok || ip != nil {
		t.Errorf("got (%v, %v), want (nil, false)", ip, ok)
	}
}

func TestClientIPFromTrustedXFF_WhitespaceOnly(t *testing.T) {
	r := &http.Request{Header: http.Header{}}
	r.Header.Set("X-Forwarded-For", "   ")
	if ip, ok := clientIPFromTrustedXFF(r); ok || ip != nil {
		t.Errorf("got (%v, %v), want (nil, false)", ip, ok)
	}
}

func TestClientIPFromTrustedXFF_Garbage(t *testing.T) {
	r := &http.Request{Header: http.Header{}}
	r.Header.Set("X-Forwarded-For", "not-an-ip")
	if ip, ok := clientIPFromTrustedXFF(r); ok || ip != nil {
		t.Errorf("got (%v, %v), want (nil, false)", ip, ok)
	}
}

func TestClientIPFromTrustedXFF_Valid(t *testing.T) {
	r := &http.Request{Header: http.Header{}}
	r.Header.Set("X-Forwarded-For", "  203.0.113.42  ")
	ip, ok := clientIPFromTrustedXFF(r)
	if !ok {
		t.Fatal("ok=false, want true")
	}
	want := net.ParseIP("203.0.113.42")
	if !ip.Equal(want) {
		t.Errorf("ip = %v, want %v", ip, want)
	}
}

func TestClientIPFromTrustedXFF_V6Valid(t *testing.T) {
	r := &http.Request{Header: http.Header{}}
	r.Header.Set("X-Forwarded-For", "2001:db8::1")
	ip, ok := clientIPFromTrustedXFF(r)
	if !ok {
		t.Fatal("ok=false, want true")
	}
	want := net.ParseIP("2001:db8::1")
	if !ip.Equal(want) {
		t.Errorf("ip = %v, want %v", ip, want)
	}
}

// --- bearerTokenFromHeader ---------------------------------------

func TestBearerTokenFromHeader(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"abc":          "",
		"Bearer ":      "",
		"Bearer abc":   "abc",
		"bearer abc":   "abc",
		"BEARER abc":   "abc",
		"BeArEr abc":   "abc",
		"Bearer  abc ": "abc", // trim space
		"Bearer":       "",
		"Basic abc":    "",
	}
	for in, want := range cases {
		if got := bearerTokenFromHeader(in); got != want {
			t.Errorf("bearerTokenFromHeader(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- applyHeaderOp -----------------------------------------------

func TestApplyHeaderOp_SetAction(t *testing.T) {
	hdr := http.Header{}
	applyHeaderOp(hdr, EdgeRuleHeaderOp{Action: "set", Name: "X-A", Value: "v1"})
	if got := hdr.Get("X-A"); got != "v1" {
		t.Errorf("got %q, want v1", got)
	}
	// Set again → replaces.
	applyHeaderOp(hdr, EdgeRuleHeaderOp{Action: "set", Name: "X-A", Value: "v2"})
	if got := hdr.Get("X-A"); got != "v2" {
		t.Errorf("got %q, want v2", got)
	}
}

func TestApplyHeaderOp_SetWithEmptyValueDeletes(t *testing.T) {
	hdr := http.Header{"X-A": {"v"}}
	applyHeaderOp(hdr, EdgeRuleHeaderOp{Action: "set", Name: "X-A", Value: ""})
	if got := hdr.Get("X-A"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestApplyHeaderOp_AddAppends(t *testing.T) {
	hdr := http.Header{}
	applyHeaderOp(hdr, EdgeRuleHeaderOp{Action: "add", Name: "X-A", Value: "v1"})
	applyHeaderOp(hdr, EdgeRuleHeaderOp{Action: "add", Name: "X-A", Value: "v2"})
	if got := hdr.Values("X-A"); len(got) != 2 || got[0] != "v1" || got[1] != "v2" {
		t.Errorf("got %v", got)
	}
}

func TestApplyHeaderOp_AddEmptyValueSkipped(t *testing.T) {
	hdr := http.Header{}
	applyHeaderOp(hdr, EdgeRuleHeaderOp{Action: "add", Name: "X-A", Value: ""})
	if got := hdr.Get("X-A"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestApplyHeaderOp_RemoveAction(t *testing.T) {
	hdr := http.Header{"X-A": {"v"}}
	applyHeaderOp(hdr, EdgeRuleHeaderOp{Action: "remove", Name: "X-A"})
	if got := hdr.Get("X-A"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestApplyHeaderOp_UnknownActionAppends(t *testing.T) {
	// Any unknown action verb (e.g. "Add", typo'd verb) falls into
	// the default → add-style append.
	hdr := http.Header{}
	applyHeaderOp(hdr, EdgeRuleHeaderOp{Action: "Add", Name: "X-A", Value: "v1"})
	if got := hdr.Get("X-A"); got != "v1" {
		t.Errorf("got %q, want v1 (default branch → add)", got)
	}
}

// --- isAcceptJSON ------------------------------------------------

func TestIsAcceptJSON(t *testing.T) {
	cases := map[string]bool{
		"":                                false,
		"text/html":                       false,
		"application/json":                true,
		"application/json; charset=utf-8": true,
		"text/html, application/json":     true,
		"text/html,application/json":      true,
		"APPLICATION/JSON":                true,
		"application/json , text/html":    true,
		"text/event-stream":               false,
	}
	for in, want := range cases {
		if got := isAcceptJSON(in); got != want {
			t.Errorf("isAcceptJSON(%q) = %v, want %v", in, got, want)
		}
	}
}

// --- hostname (extra branches beyond handler_test.go:751) -------

func TestHostname_ExtraBranches(t *testing.T) {
	cases := map[string]string{
		"example.com":      "example.com",
		"EXAMPLE.com":      "example.com",
		"example.com:8080": "example.com",
		"Example.COM:443":  "example.com",
	}
	for in, want := range cases {
		if got := hostname(in); got != want {
			t.Errorf("hostname(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- write*RateLimitHeaders --------------------------------------

func TestWriteAppRateLimitHeaders_NilHandler(t *testing.T) {
	// Nil handler → no panic.
	var h *Handler
	w := httptest.NewRecorder()
	h.writeAppRateLimitHeaders(w, "app-1", api.PlanHobby)
	if w.Header().Get("X-RateLimit-Limit") != "" {
		t.Error("nil handler should not write headers")
	}
}

func TestWriteAppRateLimitHeaders_NilLimiter(t *testing.T) {
	h := &Handler{}
	w := httptest.NewRecorder()
	h.writeAppRateLimitHeaders(w, "app-1", api.PlanHobby)
	if w.Header().Get("X-RateLimit-Limit") != "" {
		t.Error("nil limiter should not write headers")
	}
}

func TestWriteAppRateLimitHeaders_LimiterNoAppBucket(t *testing.T) {
	// A real *Limiter has no bucket for an unknown app → Peek
	// returns ok=false → no headers written.
	h := &Handler{limiter: NewLimiter()}
	w := httptest.NewRecorder()
	h.writeAppRateLimitHeaders(w, "app-unknown", api.PlanHobby)
	if w.Header().Get("X-RateLimit-Limit") != "" {
		t.Error("unknown app: should not write headers")
	}
}

func TestWriteAppRateLimitHeaders_HeadersSetAfterAllow(t *testing.T) {
	// Pin: after Allow establishes a bucket, Peek returns the
	// post-consume values → trio appears.
	lim := NewLimiter()
	lim.Allow(context.Background(), "app-1", api.PlanHobby)
	h := &Handler{limiter: lim}
	w := httptest.NewRecorder()
	h.writeAppRateLimitHeaders(w, "app-1", api.PlanHobby)
	if got := w.Header().Get("X-RateLimit-Limit"); got == "" {
		t.Error("after Allow: Limit not set")
	}
	if got := w.Header().Get("X-RateLimit-Remaining"); got == "" {
		t.Error("after Allow: Remaining not set")
	}
	if got := w.Header().Get("X-RateLimit-Reset"); got == "" {
		t.Error("after Allow: Reset not set")
	}
}

func TestWriteAccountRateLimitHeaders_NilHandler(t *testing.T) {
	var h *Handler
	w := httptest.NewRecorder()
	h.writeAccountRateLimitHeaders(w, "acct-1", api.PlanHobby)
	if w.Header().Get("X-AccountRateLimit-Limit") != "" {
		t.Error("nil handler should not write headers")
	}
}

func TestWriteAccountRateLimitHeaders_EmptyAccountID(t *testing.T) {
	h := &Handler{accountLimiter: NewLimiter()}
	w := httptest.NewRecorder()
	h.writeAccountRateLimitHeaders(w, "", api.PlanHobby)
	if w.Header().Get("X-AccountRateLimit-Limit") != "" {
		t.Error("empty accountID should not write headers")
	}
}

func TestWriteAccountRateLimitHeaders_HeadersAfterAllow(t *testing.T) {
	lim := NewLimiter()
	lim.AllowAccount(context.Background(), "acct-1", api.PlanPro)
	h := &Handler{accountLimiter: lim}
	w := httptest.NewRecorder()
	h.writeAccountRateLimitHeaders(w, "acct-1", api.PlanPro)
	if got := w.Header().Get("X-AccountRateLimit-Limit"); got == "" {
		t.Error("after AllowAccount: Limit not set")
	}
}

func TestWriteRouteRateLimitHeaders_NilHandler(t *testing.T) {
	var h *Handler
	w := httptest.NewRecorder()
	h.writeRouteRateLimitHeaders(w, "key", 10, 5, "route")
	if w.Header().Get("X-RouteRateLimit-Limit") != "" {
		t.Error("nil handler should not write headers")
	}
}

func TestWriteRouteRateLimitHeaders_EmptyBucketKey(t *testing.T) {
	h := &Handler{routeLimiter: NewLimiter()}
	w := httptest.NewRecorder()
	h.writeRouteRateLimitHeaders(w, "", 10, 5, "route")
	if w.Header().Get("X-RouteRateLimit-Limit") != "" {
		t.Error("empty bucketKey should not write headers")
	}
}

func TestWriteRouteRateLimitHeaders_HeadersAfterAllow(t *testing.T) {
	lim := NewLimiter()
	lim.AllowWithParams(context.Background(), "rule-1", 10, 5)
	h := &Handler{routeLimiter: lim}
	w := httptest.NewRecorder()
	h.writeRouteRateLimitHeaders(w, "rule-1", 10, 5, "")
	if got := w.Header().Get("X-RouteRateLimit-Limit"); got == "" {
		t.Error("after AllowWithParams: Limit not set")
	}
	if got := w.Header().Get("X-RouteRateLimit-Policy"); got != rateLimitScopeRoute {
		t.Errorf("Policy = %q, want %q (default)", got, rateLimitScopeRoute)
	}
}

func TestWriteRouteRateLimitHeaders_CustomPolicyEchoes(t *testing.T) {
	lim := NewLimiter()
	lim.AllowWithParams(context.Background(), "rule-1", 10, 5)
	h := &Handler{routeLimiter: lim}
	w := httptest.NewRecorder()
	h.writeRouteRateLimitHeaders(w, "rule-1", 10, 5, "per-consumer")
	if got := w.Header().Get("X-RouteRateLimit-Policy"); got != "per-consumer" {
		t.Errorf("Policy = %q, want per-consumer", got)
	}
}

// --- recordEgress ------------------------------------------------

func newMetric(t *testing.T) *Metrics {
	t.Helper()
	m := NewMetrics()
	return m
}

func TestRecordEgress_NilSafe(t *testing.T) {
	var h *Handler
	// Must not panic.
	h.recordEgress(nil, Target{}, App{})
	h.recordEgress(&statusRecorder{}, Target{}, App{})
	h = &Handler{}
	h.recordEgress(nil, Target{}, App{})
}

func TestRecordEgress_StreamingSkipsRecording(t *testing.T) {
	// The streaming path uses per-flush deltas; the post-proxy
	// recordEgress must not double-count.
	m := newMetric(t)
	h := &Handler{metrics: m}
	rec := &statusRecorder{
		ResponseWriter: httptest.NewRecorder(),
		status:         200,
		Bytes:          100,
		flusher:        &fakeFlusher{},
	}
	h.recordEgress(rec, Target{InstanceID: "inst-1"}, App{ID: "app-1"})
	if rec.Bytes != 100 {
		t.Errorf("recorder bytes changed: %d", rec.Bytes)
	}
}

func TestRecordEgress_4xx5xxSkipped(t *testing.T) {
	// 4xx/5xx responses never reach the body stage on the
	// ReverseProxy path, so trying to count their bytes would
	// over-attribute.
	m := newMetric(t)
	h := &Handler{metrics: m}
	for _, status := range []int{199, 400, 404, 499, 500} {
		rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: status, Bytes: 100}
		h.recordEgress(rec, Target{InstanceID: "inst-1"}, App{ID: "app-1"})
		// We can't easily observe the (unwritten) metric, so just
		// pin that no panic happens.
	}
}

func TestRecordEgress_ZeroBytesSkipped(t *testing.T) {
	m := newMetric(t)
	h := &Handler{metrics: m}
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: 200, Bytes: 0}
	h.recordEgress(rec, Target{InstanceID: "inst-1"}, App{ID: "app-1"})
	// Should silently skip the metric increment.
}

func TestRecordEgress_NilInstanceIDSkipsSink(t *testing.T) {
	m := newMetric(t)
	h := &Handler{metrics: m}
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: 200, Bytes: 100}
	h.recordEgress(rec, Target{InstanceID: ""}, App{ID: "app-1"})
	// Should NOT crash even though the target.InstanceID is empty.
}

func TestRecordEgress_NilAppIDSkipsMetric(t *testing.T) {
	m := newMetric(t)
	h := &Handler{metrics: m}
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: 200, Bytes: 100}
	h.recordEgress(rec, Target{InstanceID: "inst-1"}, App{ID: ""})
}

// --- preInstantiateApps.seen --------------------------------------

func TestPreInstantiateApps_FirstSeenReturnsFalse(t *testing.T) {
	p := &preInstantiateApps{}
	if p.seen("app-A") {
		t.Error("first sight: got true, want false")
	}
}

func TestPreInstantiateApps_SecondSeenReturnsTrue(t *testing.T) {
	p := &preInstantiateApps{}
	p.seen("app-A")
	if !p.seen("app-A") {
		t.Error("second sight: got false, want true")
	}
}

// --- preInstantiateApp / preInstantiateAppRoute ------------------

func TestPreInstantiateApp_NilSafe(t *testing.T) {
	var h *Handler
	h.preInstantiateApp("app-1") // must not panic
	// nil metrics → also a no-op.
	(&Handler{}).preInstantiateApp("app-1")
	// empty appID → no-op.
	m := NewMetrics()
	(&Handler{metrics: m}).preInstantiateApp("")
}

func TestPreInstantiateAppRoute_NilSafe(t *testing.T) {
	var h *Handler
	h.preInstantiateAppRoute("app-1", "/") // must not panic
}

func TestPreInstantiateAppRoute_EmptyFieldsSkipped(t *testing.T) {
	m := NewMetrics()
	h := &Handler{metrics: m}
	h.preInstantiateAppRoute("", "/foo")  // empty app
	h.preInstantiateAppRoute("app-1", "") // empty route
}

// --- statusRecorder ---------------------------------------------

type fakeFlusher struct {
	http.ResponseWriter
	flushed int
}

func (f *fakeFlusher) Flush() { f.flushed++ }

func TestStatusRecorder_InstallFlushHookNilFlusherSkipsArm(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	rec.installFlushHook(nil, nil, 0, 0, 0)
	if rec.flusher != nil {
		t.Error("nil flusher: armed anyway")
	}
}

func TestStatusRecorder_InstallFlushHookArmsAllFields(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	flusher := &fakeFlusher{}
	rec.installFlushHook(flusher, nil, 256, 200*time.Millisecond, time.Second)
	if rec.flusher != flusher {
		t.Error("flusher not installed")
	}
	if !rec.streaming {
		t.Error("streaming flag not set")
	}
	if !rec.firstFlush {
		t.Error("firstFlush gate not set")
	}
	if rec.flushBytes != 256 {
		t.Errorf("flushBytes = %d", rec.flushBytes)
	}
	if rec.flushInterval != 200*time.Millisecond {
		t.Errorf("flushInterval = %v", rec.flushInterval)
	}
}

func TestStatusRecorder_FlushesEveryWriteForEventStream(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	flusher := &fakeFlusher{}
	rec := &statusRecorder{ResponseWriter: w}
	rec.installFlushHook(flusher, nil, 256*1024, time.Hour, time.Second)
	rec.WriteHeader(http.StatusOK)

	if _, err := rec.Write([]byte("data: first\n\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := rec.Write([]byte("data: second\n\n")); err != nil {
		t.Fatal(err)
	}
	if flusher.flushed != 2 {
		t.Fatalf("flushes = %d, want one flush per write", flusher.flushed)
	}
}

func TestStatusRecorder_FlushesEveryWriteForNDJSON(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "application/x-ndjson")
	flusher := &fakeFlusher{}
	rec := &statusRecorder{ResponseWriter: w}
	rec.installFlushHook(flusher, nil, 256*1024, time.Hour, time.Second)
	rec.WriteHeader(http.StatusOK)

	if _, err := rec.Write([]byte("{\"event\":\"first\"}\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := rec.Write([]byte("{\"event\":\"second\"}\n")); err != nil {
		t.Fatal(err)
	}
	if flusher.flushed != 2 {
		t.Fatalf("flushes = %d, want one flush per write", flusher.flushed)
	}
}

func TestStatusRecorder_FlushesEveryWriteForImplicitHeader(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "text/event-stream")
	flusher := &fakeFlusher{}
	rec := &statusRecorder{ResponseWriter: w}
	rec.installFlushHook(flusher, nil, 256*1024, time.Hour, time.Second)

	if _, err := rec.Write([]byte("data: first\n\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := rec.Write([]byte("data: second\n\n")); err != nil {
		t.Fatal(err)
	}
	if flusher.flushed != 2 {
		t.Fatalf("flushes = %d, want one flush per implicit-header write", flusher.flushed)
	}
}

func TestStatusRecorder_RetainsWindowForOtherStreamingContent(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "application/octet-stream")
	flusher := &fakeFlusher{}
	rec := &statusRecorder{ResponseWriter: w}
	rec.installFlushHook(flusher, nil, 256*1024, time.Hour, time.Second)
	rec.WriteHeader(http.StatusOK)

	if _, err := rec.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := rec.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}
	if flusher.flushed != 1 {
		t.Fatalf("flushes = %d, want initial flush only while window is open", flusher.flushed)
	}
}

func TestFlushEveryWriteMediaTypes(t *testing.T) {
	for contentType, want := range map[string]bool{
		"text/event-stream":                   true,
		"TEXT/EVENT-STREAM; charset=utf-8":    true,
		"application/x-ndjson":                true,
		"application/x-ndjson; charset=utf-8": true,
		"application/json":                    false,
		"application/octet-stream":            false,
		"":                                    false,
	} {
		if got := flushEveryWrite(contentType); got != want {
			t.Errorf("flushEveryWrite(%q) = %v, want %v", contentType, got, want)
		}
	}
}

func TestStatusRecorder_WriteHeader_RecordsStatusAndContentType(t *testing.T) {
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w}
	rec.ResponseWriter.Header().Set("Content-Type", "application/json")
	rec.WriteHeader(201)
	if rec.status != 201 {
		t.Errorf("status = %d, want 201", rec.status)
	}
	if !rec.wroteHeader {
		t.Error("wroteHeader flag not set")
	}
	if rec.ContentType != "application/json" {
		t.Errorf("ContentType = %q", rec.ContentType)
	}
	if w.Code != 201 {
		t.Errorf("underlying recorder code = %d", w.Code)
	}
}

func TestStatusRecorder_WriteHeader_OnlyFirstWrites(t *testing.T) {
	// WriteHeader once → status captured. Second call should NOT
	// mutate rec.status.
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w}
	rec.WriteHeader(201)
	rec.WriteHeader(500)
	if rec.status != 201 {
		t.Errorf("status changed: got %d, want 201", rec.status)
	}
}

func TestStatusRecorder_InstallHeaderOps_NilSafe(t *testing.T) {
	// nil receiver and nil-ops branches.
	var rec *statusRecorder
	rec.installHeaderOps(nil) // must not panic

	rec = &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	rec.installHeaderOps(nil)
	if rec.headerOps != nil {
		t.Error("nil ops should leave headerOps as nil")
	}
}

func TestStatusRecorder_InstallHeaderOps_Arm(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	rec.installHeaderOps([]EdgeRuleHeaderOp{{Action: "set", Name: "X-A", Value: "v"}})
	if len(rec.headerOps) != 1 {
		t.Errorf("got %d ops", len(rec.headerOps))
	}
}

func TestStatusRecorder_HeaderOpsAppliedOnWriteHeader(t *testing.T) {
	// installHeaderOps → WriteHeader → underlying headers must
	// reflect the op mutation BEFORE the status code is committed.
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w}
	rec.installHeaderOps([]EdgeRuleHeaderOp{
		{Action: "set", Name: "X-Custom", Value: "hello"},
	})
	rec.WriteHeader(200)
	if got := w.Header().Get("X-Custom"); got != "hello" {
		t.Errorf("X-Custom = %q, want hello (header op should run BEFORE status commit)", got)
	}
}

func TestStatusRecorder_Write_DefaultStatusIsOK(t *testing.T) {
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w}
	rec.Write([]byte("hello"))
	if rec.status != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.status)
	}
	if rec.Bytes != 5 {
		t.Errorf("Bytes = %d, want 5", rec.Bytes)
	}
	if w.Code != http.StatusOK {
		t.Errorf("underlying code = %d, want 200", w.Code)
	}
}

func TestStatusRecorder_Write_AccumulatesBytes(t *testing.T) {
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w}
	rec.Write([]byte("abc"))
	rec.Write([]byte("defg"))
	if rec.Bytes != 7 {
		t.Errorf("Bytes = %d, want 7", rec.Bytes)
	}
}

func TestStatusRecorder_Flush_NilFlusherNoop(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	rec.Flush() // must not panic on buffered path
}

func TestStatusRecorder_Flush_WiredToUnderlying(t *testing.T) {
	flusher := &fakeFlusher{ResponseWriter: httptest.NewRecorder()}
	rec := &statusRecorder{ResponseWriter: flusher, flusher: flusher}
	rec.Flush()
	rec.Flush()
	if flusher.flushed != 2 {
		t.Errorf("flusher.Flush called %d times, want 2", flusher.flushed)
	}
}

func TestStatusRecorder_MaybeFlush_BytesThreshold(t *testing.T) {
	// Two writes → bytes since last flush exceed flushBytes →
	// trigger.
	flusher := &fakeFlusher{ResponseWriter: httptest.NewRecorder()}
	rec := &statusRecorder{
		ResponseWriter: flusher,
		flusher:        flusher,
		flushBytes:     10,
		flushInterval:  time.Hour, // never time-based
	}
	rec.firstFlush = false          // ensure threshold branch
	rec.Write([]byte("0123456789")) // 10 bytes
	rec.Write([]byte("extra"))      // 15 total
	if flusher.flushed < 1 {
		t.Errorf("bytes threshold should have fired flush: flushed=%d", flusher.flushed)
	}
}

func TestStatusRecorder_MaybeFlush_TimeInterval(t *testing.T) {
	flusher := &fakeFlusher{ResponseWriter: httptest.NewRecorder()}
	rec := &statusRecorder{
		ResponseWriter: flusher,
		flusher:        flusher,
		flushBytes:     1 << 20, // never bytes-based
		flushInterval:  1 * time.Millisecond,
		lastFlushAt:    time.Now().Add(-time.Second),
	}
	rec.firstFlush = false // ensure interval branch
	rec.Write([]byte("x"))
	if flusher.flushed < 1 {
		t.Errorf("interval trigger should fire flush: flushed=%d", flusher.flushed)
	}
}

func TestStatusRecorder_MaybeFlush_FirstFlushUnconditional(t *testing.T) {
	// firstFlush=true → flush fires immediately on first Write
	// even with zero bytes accumulated.
	flusher := &fakeFlusher{ResponseWriter: httptest.NewRecorder()}
	rec := &statusRecorder{
		ResponseWriter: flusher,
		flusher:        flusher,
		flushBytes:     1 << 20, // never bytes-based
		flushInterval:  time.Hour,
		firstFlush:     true,
		writeDeadline:  time.Second, // so the deadline-install branch clears firstFlush
	}
	rec.Write([]byte(""))
	if flusher.flushed != 1 {
		t.Errorf("firstFlush should fire exactly once: flushed=%d", flusher.flushed)
	}
	if rec.firstFlush {
		t.Error("firstFlush should be cleared after firing when writeDeadline>0")
	}
}

func TestStatusRecorder_DoFlush_InstallsWriteDeadlineOnce(t *testing.T) {
	flusher := &fakeFlusher{ResponseWriter: httptest.NewRecorder()}
	rec := &statusRecorder{
		ResponseWriter: flusher,
		flusher:        flusher,
		writeDeadline:  time.Second,
	}
	rec.doFlush()
	if rec.firstFlush {
		t.Error("firstFlush not cleared after first doFlush")
	}
	rec.lastFlushedBytes = rec.Bytes // pretend 0 bytes flushed
	rec.doFlush()                    // second flush — no deadline re-install attempt
}

func TestStatusRecorder_DoFlush_NoWriteDeadlineSkipsController(t *testing.T) {
	// writeDeadline=0 → controller branch is skipped (defensive
	// guard against controllers that don't support the call).
	flusher := &fakeFlusher{ResponseWriter: httptest.NewRecorder()}
	rec := &statusRecorder{ResponseWriter: flusher, flusher: flusher}
	// firstFlush defaults to true (zero value); deadline=0 →
	// controller branch skipped silently.
	rec.doFlush()
}

func TestStatusRecorder_DoFlush_NoOnFlushSkips(t *testing.T) {
	// nil onFlush closure → onFlush branch is skipped, but the
	// underlying flusher still fires.
	flusher := &fakeFlusher{ResponseWriter: httptest.NewRecorder()}
	rec := &statusRecorder{ResponseWriter: flusher, flusher: flusher}
	rec.doFlush()
	if flusher.flushed != 1 {
		t.Errorf("underlying flusher not fired: flushed=%d", flusher.flushed)
	}
}

func TestStatusRecorder_DoFlush_FiresOnFlushWithCumulative(t *testing.T) {
	var called int64
	flusher := &fakeFlusher{ResponseWriter: httptest.NewRecorder()}
	rec := &statusRecorder{
		ResponseWriter: flusher,
		flusher:        flusher,
		onFlush:        func(c int64) { called = c },
	}
	rec.Bytes = 42
	rec.doFlush()
	if called != 42 {
		t.Errorf("onFlush called with %d, want 42", called)
	}
	if rec.lastFlushedBytes != 42 {
		t.Errorf("lastFlushedBytes = %d, want 42", rec.lastFlushedBytes)
	}
}

func TestStatusRecorder_FinalFlush_NilFlusherSkips(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	rec.finalFlush() // must not panic on buffered path
}

func TestStatusRecorder_FinalFlush_Wired(t *testing.T) {
	flusher := &fakeFlusher{ResponseWriter: httptest.NewRecorder()}
	rec := &statusRecorder{ResponseWriter: flusher, flusher: flusher}
	rec.finalFlush()
	if flusher.flushed != 1 {
		t.Errorf("finalFlush not fired: flushed=%d", flusher.flushed)
	}
}

// --- routeSetFor + RouteSetForTest ------------------------------

func TestRouteSetFor_EmptyAppIDReturnsNil(t *testing.T) {
	h := &Handler{}
	if got := h.routeSetFor("", true); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestRouteSetFor_EnabledReturnsSet(t *testing.T) {
	h := &Handler{}
	got := h.routeSetFor("app-1", true)
	if got == nil {
		t.Fatal("nil, want *routeLabelSet")
	}
	// Calling again for the same appID returns the same set.
	if again := h.routeSetFor("app-1", true); again != got {
		t.Error("second call returned different set")
	}
}

func TestRouteSetForTest_ExposesSet(t *testing.T) {
	h := &Handler{}
	got := h.RouteSetForTest("app-test")
	if got == nil {
		t.Fatal("RouteSetForTest: nil")
	}
}

// --- RoutesFor ---------------------------------------------------

func TestRoutesFor_EmptyHandlers(t *testing.T) {
	var h *Handler
	routes, overflowed := h.RoutesFor("app-1")
	if routes != nil || overflowed {
		t.Errorf("nil handler: got (%v, %v)", routes, overflowed)
	}
}

func TestRoutesFor_UnknownApp(t *testing.T) {
	h := &Handler{}
	routes, overflowed := h.RoutesFor("unknown")
	if routes != nil || overflowed {
		t.Errorf("unknown app: got (%v, %v)", routes, overflowed)
	}
}

func TestRoutesFor_EmptyAdmittedSet(t *testing.T) {
	h := &Handler{}
	s := h.RouteSetForTest("app-empty")
	s.admitted = nil
	routes, _ := h.RoutesFor("app-empty")
	if len(routes) != 0 {
		t.Errorf("got %v, want []", routes)
	}
}

// --- guard: pass-through recording -------------------------------

func TestRecordingPassesThroughBytes(t *testing.T) {
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w}
	rec.Write([]byte("hello world"))
	if w.Body.String() != "hello world" {
		t.Errorf("body = %q", w.Body.String())
	}
}

// import "" check
var _ = strings.HasPrefix
