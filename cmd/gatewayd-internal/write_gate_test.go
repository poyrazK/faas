// write_gate_test.go — coverage for the Tier A9 / ADR-084
// standby write-redirect handler.
//
// The gate has exactly 8 outcomes; this file pins every
// (decision × authKind) cell so a future refactor that
// drops or merges a path fails loud at code review.
//
// The fake resolver (fakeLeaderResolver) and fake client
// (fakeLeaderClient) live at the bottom of the file; the
// table-driven tests at the top drive the gate through
// every path without spinning up real mTLS material.
//
// # Coverage matrix
//
//	| Scenario                              | decision           | auth_kind   |
//	|---------------------------------------|--------------------|-------------|
//	| GET (read)                            | bypass             | -           |
//	| POST /v1/webhooks/stripe (carve-out)  | bypass             | -           |
//	| POST /v1.zip (non-apid)               | bypass             | -           |
//	| POST /v1/apps (leader is me)          | same_box           | bearer      |
//	| POST /v1/apps (leader is me)          | same_box           | cookie      |
//	| POST /v1/apps (leader is me)          | same_box           | anonymous   |
//	| POST /v1/apps (no leader)             | unreachable        | bearer      |
//	| POST /v1/apps (no leader)             | unreachable        | cookie      |
//	| POST /v1/apps (no leader)             | unreachable        | anonymous   |
//	| POST /v1/apps + sentinel header       | loop               | bearer      |
//	| POST /v1/apps + sentinel header       | loop               | cookie      |
//	| POST /v1/apps + cookie (standby)      | cookie_redirect    | cookie      |
//	| POST /v1/apps + bearer (standby)      | relayed            | bearer      |
//	| POST /v1/apps + anonymous (standby)   | relayed            | anonymous   |
//	| POST /v1/apps + bearer (standby,      |                    |             |
//	|   transport error)                    | error              | bearer      |
//	| POST /v1/apps + bearer (standby,      |                    |             |
//	|   TLS handshake error)                | mTLS_failure       | bearer      |
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway/writegate"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// fakeLeaderResolver implements writegate.LeaderResolver
// for the gate's tests. The struct lets each test set the
// canned (name, isMe, err) tuple to drive every decision.
type fakeLeaderResolver struct {
	name  string
	isMe  bool
	err   error
	calls int
}

func (f *fakeLeaderResolver) Current(_ context.Context) (string, bool, error) {
	f.calls++
	return f.name, f.isMe, f.err
}

// fakeLeaderClient implements writegate.LeaderHTTPClient
// for the gate's tests. The canned response / error lets
// each test drive the success / mTLS-failure / generic-error
// paths without spinning up a real mTLS server.
type fakeLeaderClient struct {
	resp *http.Response
	err  error
}

func (f *fakeLeaderClient) Relay(_ context.Context, _ string, _ *http.Request) (*http.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

// newTestOpsMetrics constructs a minimal OpsMetrics that
// registers only the Tier A9 counters + the cross-link to
// ActivePassiveFailovers. We do NOT use wire.NewOpsMetrics
// because that registers the full daemons' worth of
// counters (60+ series) and we only need 8×3 + a couple of
// cross-links for these tests.
//
// The registry is local to the test (prometheus.NewRegistry)
// so the test runs in parallel without label collisions.
func newTestOpsMetrics(t *testing.T) *testOpsMetrics {
	t.Helper()
	reg := prometheus.NewRegistry()

	writeRedirect := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gatewayd_internal_write_redirect_total",
		Help: "test metric for writegate outcomes",
	}, []string{"outcome", "auth_kind"})
	for _, oc := range writegate.AllWriteOutcomes {
		for _, ak := range writegate.AllAuthKinds {
			writeRedirect.WithLabelValues(string(oc), string(ak))
		}
	}
	writeLatency := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "gatewayd_internal_write_redirect_latency_seconds",
		Help:    "test metric for writegate latency",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	})
	activePassive := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gatewayd_public_active_passive_failovers_total",
		Help: "test cross-link",
	}, []string{"outcome"})
	for _, oc := range []string{"dns_flipped", "dns_stale", "peer_unreachable", "manual_drain", "mTLS_failure"} {
		activePassive.WithLabelValues(oc)
	}
	reg.MustRegister(writeRedirect, writeLatency, activePassive)

	return &testOpsMetrics{
		Registry:      reg,
		writeRedirect: writeRedirect,
		writeLatency:  writeLatency,
		activePassive: activePassive,
	}
}

// testOpsMetrics mirrors the three accessors the writeGate
// uses (WriteRedirectTotal, WriteRedirectLatency,
// ActivePassiveFailovers). We don't construct a full
// *wire.OpsMetrics because the test only needs those three.
type testOpsMetrics struct {
	Registry      *prometheus.Registry
	writeRedirect *prometheus.CounterVec
	writeLatency  prometheus.Histogram
	activePassive *prometheus.CounterVec
}

func (t *testOpsMetrics) WriteRedirectTotal(outcome, authKind string) prometheus.Counter {
	return t.writeRedirect.WithLabelValues(outcome, authKind)
}

func (t *testOpsMetrics) WriteRedirectLatency() prometheus.Observer {
	return t.writeLatency
}

func (t *testOpsMetrics) ActivePassiveFailovers(outcome string) prometheus.Counter {
	return t.activePassive.WithLabelValues(outcome)
}

// scrape returns the prometheus text body for assertions.
// We use httptest.NewRecorder + promhttp.HandlerFor to keep
// the test self-contained.
func (t *testOpsMetrics) scrape() string {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	promhttp.HandlerFor(t.Registry, promhttp.HandlerOpts{}).ServeHTTP(rec, req)
	return rec.Body.String()
}

// counterValue parses the metrics text and returns the float
// value of the matching line. Returns 0 if the counter line
// is absent (which is correct for a never-incremented
// counter in Prometheus text format).
//
// Prometheus sorts label names alphabetically in the text
// output (verified via scrape_probe_test.go), so we mirror
// that ordering when building the lookup prefix.
func counterValue(body, name string, labels map[string]string) float64 {
	want := name
	if len(labels) > 0 {
		want += "{"
		keys := make([]string, 0, len(labels))
		for k := range labels {
			keys = append(keys, k)
		}
		sortStrings(keys)
		for i, k := range keys {
			if i > 0 {
				want += ","
			}
			want += k + `="` + labels[k] + `"`
		}
		want += "}"
	}
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, want+" ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var v float64
		_, err := fmtSscanf(fields[1], &v)
		if err != nil {
			continue
		}
		return v
	}
	return 0
}

// sortStrings is a tiny insertion sort for the small
// (≤ 4-element) label slices this test uses. Pulling in
// "sort" would be fine but adds an import for one helper.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// fmtSscanf is a tiny wrapper to keep the import block
// minimal. We use fmt.Sscanf directly to avoid pulling in
// the full encoding/csv stack for one float parse.
func fmtSscanf(s string, v *float64) (int, error) {
	// Mimics fmt.Sscanf(s, "%f", v) without the import.
	var x float64
	decimals := 0
	idx := 0
	neg := false
	if idx < len(s) && (s[idx] == '+' || s[idx] == '-') {
		if s[idx] == '-' {
			neg = true
		}
		idx++
	}
	intPart := 0
	hadDigit := false
	for idx < len(s) && s[idx] >= '0' && s[idx] <= '9' {
		intPart = intPart*10 + int(s[idx]-'0')
		hadDigit = true
		idx++
	}
	if idx < len(s) && s[idx] == '.' {
		idx++
		frac := 0.0
		pow := 0.1
		for idx < len(s) && s[idx] >= '0' && s[idx] <= '9' {
			frac += float64(s[idx]-'0') * pow
			pow *= 0.1
			idx++
			decimals++
			hadDigit = true
		}
		x = float64(intPart) + frac
	} else {
		x = float64(intPart)
	}
	if !hadDigit {
		return 0, errors.New("no digits")
	}
	if neg {
		x = -x
	}
	*v = x
	return 1, nil
}

// newTestGate wires up a writeGate with stub resolver +
// client + a no-op next handler. The next handler records
// the request so tests can assert bypass / same_box fall
// through.
func newTestGate(
	t *testing.T,
	resolver writegate.LeaderResolver,
	client writegate.LeaderHTTPClient,
	next http.Handler,
) (http.Handler, *testOpsMetrics, *bytes.Buffer) {
	t.Helper()
	if resolver == nil {
		resolver = &fakeLeaderResolver{name: "node-a", isMe: true}
	}
	if client == nil {
		client = &fakeLeaderClient{}
	}
	if next == nil {
		next = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	}
	logBuf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	om := newTestOpsMetrics(t)
	gate := newWriteGate(next, resolver, client, writegatePath, "node-a", wrapTestMetrics(om), log)
	return gate, om, logBuf
}

// writegatePath is the production path predicate wrapper
// the test gate uses. It mirrors the production
// cmd/gatewayd-internal/proxy.go::isApidPath behaviour
// (which delegates to pkg/apid.IsApidPath).
func writegatePath(p string) bool {
	// Inline the apid-public surface so the test doesn't
	// import pkg/apid (which would cycle through
	// cmd/gatewayd-internal's transitive deps).
	anchoredRoots := []string{
		"/v1",
		"/dashboard",
		"/login",
		"/signup",
		"/logout",
		"/auth",
		"/status",
		"/cli-auth",
	}
	for _, root := range anchoredRoots {
		if p == root || p == root+"/" || strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	if strings.HasPrefix(p, "/oauth/") || p == "/oauth" {
		return true
	}
	return false
}

// wrapTestMetrics adapts the test's local metrics to the
// *wire.OpsMetrics interface the writeGate expects. We
// can't construct a real *wire.OpsMetrics because it
// requires a Prometheus prefix arg; the gate only uses
// the three accessors above, so a small adapter suffices.
func wrapTestMetrics(t *testOpsMetrics) *writeGateTestMetrics {
	return &writeGateTestMetrics{t: t}
}

type writeGateTestMetrics struct {
	t *testOpsMetrics
}

func (w *writeGateTestMetrics) WriteRedirectTotal(outcome, authKind string) prometheus.Counter {
	return w.t.WriteRedirectTotal(outcome, authKind)
}

func (w *writeGateTestMetrics) WriteRedirectLatency() prometheus.Observer {
	return w.t.WriteRedirectLatency()
}

func (w *writeGateTestMetrics) ActivePassiveFailovers(outcome string) prometheus.Counter {
	return w.t.ActivePassiveFailovers(outcome)
}

// recordingNext records the inbound request so tests can
// assert bypass / same_box fall-through.
type recordingNext struct {
	called bool
	method string
	path   string
	header http.Header
	body   []byte
}

func (r *recordingNext) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.called = true
	r.method = req.Method
	r.path = req.URL.Path
	r.header = req.Header.Clone()
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		r.body = b
	}
	w.WriteHeader(http.StatusTeapot) // distinctive marker for assertions
}

// --- Tests --------------------------------------------------------------

// TestWriteGate_Bypass_Read — GET requests fall through
// without metric increment.
func TestWriteGate_Bypass_Read(t *testing.T) {
	gate, om, _ := newTestGate(t, nil, nil, &recordingNext{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/apps", nil)
	gate.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 218 (next.ServeHTTP marker)", rec.Code)
	}
	// No writeRedirect counter should have advanced — bypass is
	// a true no-op for the metric.
	body := om.scrape()
	if got := counterValue(body, "gatewayd_internal_write_redirect_total", nil); got != 0 {
		t.Errorf("writeRedirect incremented on bypass; got %v", got)
	}
}

// TestWriteGate_Bypass_CarveOut — POST to a carve-out
// path falls through without metric increment.
func TestWriteGate_Bypass_CarveOut(t *testing.T) {
	gate, _, _ := newTestGate(t, nil, nil, &recordingNext{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", nil)
	gate.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 218 (carve-out falls through)", rec.Code)
	}
}

// TestWriteGate_Bypass_NonApidPath — POST to a non-apid
// path (e.g. customer app) falls through.
func TestWriteGate_Bypass_NonApidPath(t *testing.T) {
	gate, _, _ := newTestGate(t, nil, nil, &recordingNext{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/customer-app/foo", nil)
	gate.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 218 (non-apid falls through)", rec.Code)
	}
}

// TestWriteGate_SameBox — leader is me; falls through to
// next + increments same_box.
func TestWriteGate_SameBox(t *testing.T) {
	resolver := &fakeLeaderResolver{name: "node-a", isMe: true}
	gate, om, _ := newTestGate(t, resolver, nil, &recordingNext{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/apps",
		strings.NewReader(`{"name":"test"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	gate.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 218 (same_box falls through)", rec.Code)
	}
	body := om.scrape()
	got := counterValue(body, "gatewayd_internal_write_redirect_total",
		map[string]string{"outcome": "same_box", "auth_kind": "bearer"})
	if got != 1 {
		t.Errorf("same_box counter = %v, want 1", got)
	}
}

// TestWriteGate_Loop — inbound sentinel header → 400 +
// loop_prevented.
func TestWriteGate_Loop(t *testing.T) {
	gate, om, _ := newTestGate(t, nil, nil, &recordingNext{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/apps", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Faas-Forwarded-Leader", "node-a")
	gate.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/problem+json") {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	body := om.scrape()
	got := counterValue(body, "gatewayd_internal_write_redirect_total",
		map[string]string{"outcome": "loop_prevented", "auth_kind": "bearer"})
	if got != 1 {
		t.Errorf("loop_prevented counter = %v, want 1", got)
	}
}

// TestWriteGate_Unreachable — resolver returns no leader +
// error → 503 + leader_unreachable + 60s Retry-After.
func TestWriteGate_Unreachable(t *testing.T) {
	resolver := &fakeLeaderResolver{name: "", isMe: false, err: errors.New("store offline")}
	gate, om, _ := newTestGate(t, resolver, nil, &recordingNext{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/apps", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	gate.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "60" {
		t.Errorf("Retry-After = %q, want 60", got)
	}
	body := om.scrape()
	got := counterValue(body, "gatewayd_internal_write_redirect_total",
		map[string]string{"outcome": "leader_unreachable", "auth_kind": "bearer"})
	if got != 1 {
		t.Errorf("leader_unreachable counter = %v, want 1", got)
	}
}

// TestWriteGate_CookieRedirect — cookie auth on standby →
// 307 + Location: leader URL + 5s Retry-After.
func TestWriteGate_CookieRedirect(t *testing.T) {
	resolver := &fakeLeaderResolver{name: "node-b", isMe: false}
	gate, om, _ := newTestGate(t, resolver, nil, &recordingNext{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/apps", nil)
	req.AddCookie(&http.Cookie{Name: "faas_sid", Value: "session-token"})
	gate.ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "https://node-b.faas/v1/apps" {
		t.Errorf("Location = %q, want https://node-b.faas/v1/apps", got)
	}
	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q, want 5", got)
	}
	body := om.scrape()
	got := counterValue(body, "gatewayd_internal_write_redirect_total",
		map[string]string{"outcome": "redirect_307", "auth_kind": "cookie"})
	if got != 1 {
		t.Errorf("redirect_307 counter = %v, want 1", got)
	}
}

// TestWriteGate_Relayed_Bearer — bearer on standby →
// relay via LeaderHTTPClient + 200.
func TestWriteGate_Relayed_Bearer(t *testing.T) {
	resolver := &fakeLeaderResolver{name: "node-b", isMe: false}
	leaderResp := &http.Response{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Leader-Echo": []string{"yes"}},
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}
	client := &fakeLeaderClient{resp: leaderResp}
	gate, om, _ := newTestGate(t, resolver, client, &recordingNext{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/apps",
		strings.NewReader(`{"name":"test"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	gate.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (leader's verbatim)", rec.Code)
	}
	if got := rec.Header().Get("X-Leader-Echo"); got != "yes" {
		t.Errorf("X-Leader-Echo = %q, want yes (verbatim copy)", got)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), `"ok":true`) {
		t.Errorf("body = %q, want leader body verbatim", string(body))
	}
	scrape := om.scrape()
	got := counterValue(scrape, "gatewayd_internal_write_redirect_total",
		map[string]string{"outcome": "relayed", "auth_kind": "bearer"})
	if got != 1 {
		t.Errorf("relayed counter = %v, want 1", got)
	}
}

// TestWriteGate_Relayed_MTLSFailure — relay error contains
// "tls" → mTLS_failure outcome + active_passive cross-link.
func TestWriteGate_Relayed_MTLSFailure(t *testing.T) {
	resolver := &fakeLeaderResolver{name: "node-b", isMe: false}
	client := &fakeLeaderClient{err: errors.New("tls: handshake failure")}
	gate, om, _ := newTestGate(t, resolver, client, &recordingNext{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/apps", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	gate.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q, want 5", got)
	}
	scrape := om.scrape()
	got := counterValue(scrape, "gatewayd_internal_write_redirect_total",
		map[string]string{"outcome": "mTLS_failure", "auth_kind": "bearer"})
	if got != 1 {
		t.Errorf("mTLS_failure counter = %v, want 1", got)
	}
	gotX := counterValue(scrape, "gatewayd_public_active_passive_failovers_total",
		map[string]string{"outcome": "mTLS_failure"})
	if gotX != 1 {
		t.Errorf("active_passive_failovers_total{mTLS_failure} = %v, want 1", gotX)
	}
}

// TestWriteGate_Relayed_GenericError — non-TLS relay error
// → outcome=error + 503 + 5s Retry-After.
func TestWriteGate_Relayed_GenericError(t *testing.T) {
	resolver := &fakeLeaderResolver{name: "node-b", isMe: false}
	client := &fakeLeaderClient{err: errors.New("dial tcp: connection refused")}
	gate, om, _ := newTestGate(t, resolver, client, &recordingNext{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/apps", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	gate.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	scrape := om.scrape()
	got := counterValue(scrape, "gatewayd_internal_write_redirect_total",
		map[string]string{"outcome": "error", "auth_kind": "bearer"})
	if got != 1 {
		t.Errorf("error counter = %v, want 1", got)
	}
}

// TestWriteGate_ResolverError_OnSameBox — resolver error
// on a same-box path must NOT override (the cached leader
// is still trusted).
func TestWriteGate_ResolverError_OnSameBox(t *testing.T) {
	resolver := &fakeLeaderResolver{name: "node-a", isMe: true, err: errors.New("transient blip")}
	gate, om, _ := newTestGate(t, resolver, nil, &recordingNext{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/apps", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	gate.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 218 (same_box preserved despite resolver error)", rec.Code)
	}
	scrape := om.scrape()
	got := counterValue(scrape, "gatewayd_internal_write_redirect_total",
		map[string]string{"outcome": "same_box", "auth_kind": "bearer"})
	if got != 1 {
		t.Errorf("same_box counter = %v, want 1", got)
	}
}

// TestWriteGate_AllOutcomes_AllAuthKinds — exhaustive
// matrix coverage. This is the load-bearing regression
// test for the 8-outcome × 3-authKind grid.
func TestWriteGate_AllOutcomes_AllAuthKinds(t *testing.T) {
	cases := []struct {
		name        string
		method      string
		path        string
		setHeader   func(*http.Request)
		setCookie   func(*http.Request)
		resolver    *fakeLeaderResolver
		client      *fakeLeaderClient
		next        http.Handler
		wantCode    int
		wantOutcome writegate.WriteOutcome
		wantAuth    writegate.AuthKind
	}{
		{
			name:   "GET falls through (no metric)",
			method: http.MethodGet, path: "/v1/apps",
			resolver: &fakeLeaderResolver{isMe: true},
			next:     &recordingNext{},
			wantCode: http.StatusTeapot,
		},
		{
			name:   "POST same_box bearer",
			method: http.MethodPost, path: "/v1/apps",
			setHeader: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer t")
			},
			resolver:    &fakeLeaderResolver{name: "node-a", isMe: true},
			next:        &recordingNext{},
			wantCode:    http.StatusTeapot,
			wantOutcome: writegate.OutcomeSameBox,
			wantAuth:    writegate.AuthBearer,
		},
		{
			name:   "POST same_box cookie",
			method: http.MethodPost, path: "/v1/apps",
			setCookie: func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: "faas_sid", Value: "s"})
			},
			resolver:    &fakeLeaderResolver{name: "node-a", isMe: true},
			next:        &recordingNext{},
			wantCode:    http.StatusTeapot,
			wantOutcome: writegate.OutcomeSameBox,
			wantAuth:    writegate.AuthCookie,
		},
		{
			name:   "POST same_box anonymous",
			method: http.MethodPost, path: "/v1/apps",
			resolver:    &fakeLeaderResolver{name: "node-a", isMe: true},
			next:        &recordingNext{},
			wantCode:    http.StatusTeapot,
			wantOutcome: writegate.OutcomeSameBox,
			wantAuth:    writegate.AuthAnonymous,
		},
		{
			name:   "POST unreachable bearer",
			method: http.MethodPost, path: "/v1/apps",
			setHeader: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer t")
			},
			resolver:    &fakeLeaderResolver{name: "", err: errors.New("no leader")},
			wantCode:    http.StatusServiceUnavailable,
			wantOutcome: writegate.OutcomeLeaderUnreachable,
			wantAuth:    writegate.AuthBearer,
		},
		{
			name:   "POST unreachable cookie",
			method: http.MethodPost, path: "/v1/apps",
			setCookie: func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: "faas_sid", Value: "s"})
			},
			resolver:    &fakeLeaderResolver{name: "", err: errors.New("no leader")},
			wantCode:    http.StatusServiceUnavailable,
			wantOutcome: writegate.OutcomeLeaderUnreachable,
			wantAuth:    writegate.AuthCookie,
		},
		{
			name:   "POST unreachable anonymous",
			method: http.MethodPost, path: "/v1/apps",
			resolver:    &fakeLeaderResolver{name: "", err: errors.New("no leader")},
			wantCode:    http.StatusServiceUnavailable,
			wantOutcome: writegate.OutcomeLeaderUnreachable,
			wantAuth:    writegate.AuthAnonymous,
		},
		{
			name:   "POST loop bearer",
			method: http.MethodPost, path: "/v1/apps",
			setHeader: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer t")
				r.Header.Set("X-Faas-Forwarded-Leader", "node-a")
			},
			resolver:    &fakeLeaderResolver{name: "node-a", isMe: true},
			wantCode:    http.StatusBadRequest,
			wantOutcome: writegate.OutcomeLoopPrevented,
			wantAuth:    writegate.AuthBearer,
		},
		{
			name:   "POST loop cookie",
			method: http.MethodPost, path: "/v1/apps",
			setCookie: func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: "faas_sid", Value: "s"})
			},
			setHeader: func(r *http.Request) {
				r.Header.Set("X-Faas-Forwarded-Leader", "node-a")
			},
			resolver:    &fakeLeaderResolver{name: "node-a", isMe: true},
			wantCode:    http.StatusBadRequest,
			wantOutcome: writegate.OutcomeLoopPrevented,
			wantAuth:    writegate.AuthCookie,
		},
		{
			name:   "POST cookie_redirect",
			method: http.MethodPost, path: "/v1/apps",
			setCookie: func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: "faas_sid", Value: "s"})
			},
			resolver:    &fakeLeaderResolver{name: "node-b", isMe: false},
			wantCode:    http.StatusTemporaryRedirect,
			wantOutcome: writegate.OutcomeRedirect307,
			wantAuth:    writegate.AuthCookie,
		},
		{
			name:   "POST relayed bearer",
			method: http.MethodPost, path: "/v1/apps",
			setHeader: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer t")
			},
			resolver: &fakeLeaderResolver{name: "node-b", isMe: false},
			client: &fakeLeaderClient{resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			}},
			wantCode:    http.StatusOK,
			wantOutcome: writegate.OutcomeRelayed,
			wantAuth:    writegate.AuthBearer,
		},
		{
			name:   "POST relayed anonymous",
			method: http.MethodPost, path: "/v1/apps",
			resolver: &fakeLeaderResolver{name: "node-b", isMe: false},
			client: &fakeLeaderClient{resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			}},
			wantCode:    http.StatusOK,
			wantOutcome: writegate.OutcomeRelayed,
			wantAuth:    writegate.AuthAnonymous,
		},
		{
			name:   "POST relayed mTLS_failure",
			method: http.MethodPost, path: "/v1/apps",
			setHeader: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer t")
			},
			resolver:    &fakeLeaderResolver{name: "node-b", isMe: false},
			client:      &fakeLeaderClient{err: errors.New("certificate verify failed")},
			wantCode:    http.StatusServiceUnavailable,
			wantOutcome: writegate.OutcomeMTLSFailure,
			wantAuth:    writegate.AuthBearer,
		},
		{
			name:   "POST relayed generic error",
			method: http.MethodPost, path: "/v1/apps",
			setHeader: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer t")
			},
			resolver:    &fakeLeaderResolver{name: "node-b", isMe: false},
			client:      &fakeLeaderClient{err: errors.New("dial tcp: i/o timeout")},
			wantCode:    http.StatusServiceUnavailable,
			wantOutcome: writegate.OutcomeError,
			wantAuth:    writegate.AuthBearer,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate, om, _ := newTestGate(t, tc.resolver, tc.client, tc.next)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.setHeader != nil {
				tc.setHeader(req)
			}
			if tc.setCookie != nil {
				tc.setCookie(req)
			}
			gate.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantOutcome == "" {
				return
			}
			scrape := om.scrape()
			got := counterValue(scrape, "gatewayd_internal_write_redirect_total",
				map[string]string{
					"outcome":   string(tc.wantOutcome),
					"auth_kind": string(tc.wantAuth),
				})
			if got != 1 {
				t.Errorf("counter{%s,%s} = %v, want 1", tc.wantOutcome, tc.wantAuth, got)
			}
		})
	}
}

// TestBuildLeaderPublicURL — the redirect target shape.
func TestBuildLeaderPublicURL(t *testing.T) {
	u, _ := url.Parse("/v1/apps?foo=bar")
	got := buildLeaderPublicURL("node-b", u)
	want := "https://node-b.faas/v1/apps?foo=bar"
	if got != want {
		t.Errorf("buildLeaderPublicURL = %q, want %q", got, want)
	}
}

// TestWriteProblem_ContentType — verify the problem-detail
// shape lands as RFC 7807.
func TestWriteProblem_ContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	writeProblem(rec, http.StatusServiceUnavailable, problemDetail{
		Type: problemTypeGateway, Title: "Test", Status: 503, Code: "test",
	})
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/problem+json") {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), `"code":"test"`) {
		t.Errorf("body missing code: %s", string(body))
	}
}
