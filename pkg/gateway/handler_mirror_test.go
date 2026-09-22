// handler_mirror_test.go — issue #72 / ADR-124 / ADR-125 PR-A3
// adr: 133
//
// Whitebox trait tests for the handler fan-out of the mirror
// dispatch goroutine. The trait contracts:
//
//   - When Backend.LookupMirrorRules returns rules + true, the
//     handler fires one goroutine per rule whose `Percent` is
//     sampled true (Percent==0 disables the rule entirely).
//   - When LookupMirrorRules returns (nil, false), the handler
//     fans out zero goroutines — cache-miss is treated as
//     "no mirror", not an error.
//   - The dispatch goroutine reads from the cache, not the
//     store, so a cache miss does not block the customer request.
//   - The mirror goroutine is fire-and-forget; the customer
//     request returns regardless of mirror outcome.
//
// The actual mirror HTTP round-trip and ledger write are covered
// by the e2e suite in cmd/e2e/traffic_mirror_e2e_test.go.

package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// mirrorFakeBackend (issue #72 / ADR-124 PR-A3) extends the
// shared fakeBackend shape with mirror-aware fields. It records
// ScheduleMirror invocations, exposes a static mirror rule set
// via LookupMirrorRules, and accepts an injected
// MirrorRoundTripper so the dispatch goroutine's HTTP forward
// can be asserted deterministically.
type mirrorFakeBackend struct {
	app          App
	host         string
	upstreamAddr string

	mu          sync.Mutex
	mirrorRules []MirrorRuleRow
	// scheduleCalls counts ScheduleMirror invocations on this
	// backend; tests assert the dispatch goroutine fired once
	// per customer request (modulo percent sampling).
	scheduleCalls atomic.Int32
	// scheduleRuleIDs records the rule IDs that received a
	// ScheduleMirror call — used to assert fan-out-per-rule.
	scheduleRuleIDs []string
	// scheduleDeployments is the mirrorDeploymentID passed in
	// each call; used to assert the goroutine threaded the rule's
	// deployment, not the source's.
	scheduleDeployments []string
	// appLookupHits counts LookupMirrorRules invocations on this
	// backend; tests assert the fan-out consulted the cache.
	appLookupHits atomic.Int32

	scheduleErr error
}

type mirrorTargetFakeBackend struct {
	*mirrorFakeBackend
	target Target
}

func (m *mirrorTargetFakeBackend) ScheduleMirrorTarget(_ context.Context, _, mirrorDeploymentID, ruleID string) (Target, error) {
	m.scheduleCalls.Add(1)
	m.mu.Lock()
	m.scheduleRuleIDs = append(m.scheduleRuleIDs, ruleID)
	m.scheduleDeployments = append(m.scheduleDeployments, mirrorDeploymentID)
	m.mu.Unlock()
	if m.scheduleErr != nil {
		return Target{}, m.scheduleErr
	}
	return m.target, nil
}

func (m *mirrorFakeBackend) Lookup(_ context.Context, host string) (App, bool) {
	if host == m.host {
		return m.app, true
	}
	return App{}, false
}

func (m *mirrorFakeBackend) Pick(_ string) PickResult {
	return PickResult{
		Target: Target{
			NodeID:       m.upstreamAddr,
			InstanceID:   "i-source",
			DeploymentID: "dep-A",
			WakeID:       "wake-source",
		},
		OK:     true,
		Picked: "dep-A",
	}
}

func (m *mirrorFakeBackend) HealthyCount(_ string) int { return 1 }

func (m *mirrorFakeBackend) Admit(_ context.Context, _, _, _, _ string, _ int) (string, WakeMethod, bool, error) {
	return "wake-source", WakeMethodColdBoot, false, nil
}

func (m *mirrorFakeBackend) LookupMirrorRules(_ context.Context, _ string) ([]MirrorRuleRow, bool) {
	m.appLookupHits.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.mirrorRules) == 0 {
		return nil, false
	}
	out := make([]MirrorRuleRow, len(m.mirrorRules))
	copy(out, m.mirrorRules)
	return out, true
}

func (m *mirrorFakeBackend) ScheduleMirror(_ context.Context, _, mirrorDeploymentID, ruleID string) (string, string, error) {
	m.scheduleCalls.Add(1)
	m.mu.Lock()
	m.scheduleRuleIDs = append(m.scheduleRuleIDs, ruleID)
	m.scheduleDeployments = append(m.scheduleDeployments, mirrorDeploymentID)
	m.mu.Unlock()
	if m.scheduleErr != nil {
		return "", "", m.scheduleErr
	}
	return "mirror-instance-" + ruleID, "mirror-wake-" + ruleID, nil
}

// stubMirrorRoundTripper (issue #72 / ADR-124 PR-A3) is the
// injected MirrorRoundTripper — returns a canned 200 with a
// known body so the dispatch goroutine's classify + metric
// path runs end-to-end without standing up an httptest.Server.
type stubMirrorRoundTripper struct {
	mu      sync.Mutex
	calls   int32
	paths   []string
	headers []http.Header
	// cannedResponse is what RoundTripMirror returns. Tests
	// flip body / status to drive ClassifyResult branches.
	cannedResponse *http.Response
	cannedErr      error
}

func (s *stubMirrorRoundTripper) RoundTripMirror(_ context.Context, _ *url.URL, req *http.Request) (*http.Response, error) {
	atomic.AddInt32(&s.calls, 1)
	s.mu.Lock()
	s.paths = append(s.paths, req.URL.Path)
	s.headers = append(s.headers, req.Header.Clone())
	s.mu.Unlock()
	if s.cannedErr != nil {
		return nil, s.cannedErr
	}
	return s.cannedResponse, nil
}

// waitForMirrorCalls polls until the schedule count reaches at
// least n or the deadline expires. Mirrors goroutines fire
// asynchronously; tests need a deterministic join.
func waitForMirrorCalls(t *testing.T, b *mirrorFakeBackend, n int32, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if b.scheduleCalls.Load() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("ScheduleMirror calls = %d, want ≥%d within %v", b.scheduleCalls.Load(), n, deadline)
}

// TestHandler_MirrorFanout_SpawnsGoroutine pins the
// happy-path: one rule with Percent=100 fires exactly one
// goroutine per customer request. The dispatch goroutine
// calls ScheduleMirror once and the injected round-tripper
// returns a 200.
func TestHandler_MirrorFanout_SpawnsGoroutine(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("source response"))
	}))
	t.Cleanup(upstream.Close)

	b := &mirrorFakeBackend{
		app: App{
			ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro,
			PublicAuth: PublicAuthConfig{Mode: api.AppPublicAuthModeOpen},
		},
		host:         "jane-api.apps.dom",
		upstreamAddr: upstream.Listener.Addr().String(),
		mirrorRules: []MirrorRuleRow{
			{ID: "rule-1", AppID: "app-1", MirrorDeploymentID: "dep-B", Percent: 100},
		},
	}
	rt := &stubMirrorRoundTripper{
		cannedResponse: &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(stringBody("mirror response")),
		},
	}

	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.WithMirrorRoundTripper(rt)

	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	waitForMirrorCalls(t, b, 1, 2*time.Second)
	waitForRTCalls(t, rt, 1, 2*time.Second)
	rt.mu.Lock()
	mirrorHeaders := rt.headers[len(rt.headers)-1].Clone()
	rt.mu.Unlock()
	for name, want := range map[string]string{
		api.AppIDHeader:        "app-1",
		api.DeploymentIDHeader: "dep-B",
		api.TenantIDHeader:     "acct-1",
		api.InstanceIDHeader:   "mirror-instance-rule-1",
	} {
		if got := mirrorHeaders.Get(name); got != want {
			t.Errorf("mirror %s = %q, want %q", name, got, want)
		}
	}

	if got := b.appLookupHits.Load(); got != 1 {
		t.Errorf("LookupMirrorRules calls = %d, want 1 (handler must consult cache once)", got)
	}
	if got := b.scheduleRuleIDs[0]; got != "rule-1" {
		t.Errorf("scheduleRuleIDs[0] = %q, want rule-1", got)
	}
	if got := b.scheduleDeployments[0]; got != "dep-B" {
		t.Errorf("scheduleDeployments[0] = %q, want dep-B", got)
	}
}

func TestHandler_MirrorFanout_PreservesSourceRequestBody(t *testing.T) {
	const payload = `{"feature":"mirror-body"}`
	sourceBody := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read source body: %v", err)
		}
		sourceBody <- string(body)
		_, _ = w.Write([]byte("source response"))
	}))
	t.Cleanup(upstream.Close)

	b := &mirrorFakeBackend{
		app: App{
			ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro,
			PublicAuth: PublicAuthConfig{Mode: api.AppPublicAuthModeOpen},
		},
		host:         "jane-api.apps.dom",
		upstreamAddr: upstream.Listener.Addr().String(),
		mirrorRules: []MirrorRuleRow{
			{ID: "rule-1", AppID: "app-1", MirrorDeploymentID: "dep-B", Percent: 100},
		},
	}
	rt := &stubMirrorRoundTripper{
		cannedResponse: &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(stringBody("mirror response")),
		},
	}

	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.WithMirrorRoundTripper(rt)
	req := httptest.NewRequest(http.MethodPost, "http://jane-api.apps.dom/", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	select {
	case got := <-sourceBody:
		if got != payload {
			t.Fatalf("source body = %q, want %q", got, payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("source request did not reach upstream")
	}
	waitForMirrorCalls(t, b, 1, 2*time.Second)
}

func TestHandler_MirrorFanout_ForwardsToAdmittedMirrorTarget(t *testing.T) {
	base := &mirrorFakeBackend{
		app: App{
			ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro,
			PublicAuth: PublicAuthConfig{Mode: api.AppPublicAuthModeOpen},
		},
		host:         "jane-api.apps.dom",
		upstreamAddr: "source-node",
		mirrorRules: []MirrorRuleRow{
			{ID: "rule-1", AppID: "app-1", MirrorDeploymentID: "dep-B", Percent: 100},
		},
	}
	b := &mirrorTargetFakeBackend{
		mirrorFakeBackend: base,
		target: Target{
			AppID:        "app-1",
			NodeID:       "mirror-node",
			InstanceID:   "i-mirror",
			DeploymentID: "dep-B",
			Port:         9090,
		},
	}

	mirrorForward := make(chan Target, 1)
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.WithForwarding(func(target Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if target.InstanceID == "i-mirror" {
				if got := r.Header.Get("x-faas-instance"); got != "i-mirror" {
					t.Errorf("mirror x-faas-instance = %q, want i-mirror", got)
				}
				mirrorForward <- target
				_, _ = w.Write([]byte("mirror response"))
				return
			}
			_, _ = w.Write([]byte("source response"))
		})
	})

	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	select {
	case target := <-mirrorForward:
		if target.NodeID != "mirror-node" || target.DeploymentID != "dep-B" || target.Port != 9090 {
			t.Fatalf("mirror target = %+v, want admitted dep-B target", target)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mirror request was not forwarded")
	}
}

func TestSnapshotSourceBody_RestoresBodyBeyondSnapshotCap(t *testing.T) {
	payload := strings.Repeat("x", api.MirrorBodySnapshotCap+1024)
	req := httptest.NewRequest(http.MethodPost, "http://example.test/", strings.NewReader(payload))

	snapshot, restore := snapshotSourceBody(req)
	if len(snapshot) != api.MirrorBodySnapshotCap {
		t.Fatalf("snapshot length = %d, want %d", len(snapshot), api.MirrorBodySnapshotCap)
	}
	restore()
	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Fatalf("restored body length = %d, want %d", len(got), len(payload))
	}
}

// TestHandler_MirrorFanout_PercentZero pins that a rule with
// Percent=0 spawns no goroutine. The fan-out short-circuits
// before sampling; ScheduleMirror is never called.
func TestHandler_MirrorFanout_PercentZero(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("source"))
	}))
	t.Cleanup(upstream.Close)

	b := &mirrorFakeBackend{
		app: App{
			ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro,
			PublicAuth: PublicAuthConfig{Mode: api.AppPublicAuthModeOpen},
		},
		host:         "jane-api.apps.dom",
		upstreamAddr: upstream.Listener.Addr().String(),
		mirrorRules: []MirrorRuleRow{
			{ID: "rule-1", AppID: "app-1", MirrorDeploymentID: "dep-B", Percent: 0},
		},
	}
	rt := &stubMirrorRoundTripper{
		cannedResponse: &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(stringBody("unused")),
		},
	}

	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.WithMirrorRoundTripper(rt)

	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Give the fanout a moment to NOT fire (sampling short-circuit).
	time.Sleep(50 * time.Millisecond)
	if got := b.scheduleCalls.Load(); got != 0 {
		t.Errorf("ScheduleMirror calls = %d, want 0 (Percent=0 must skip fan-out)", got)
	}
	if got := rt.calls; got != 0 {
		t.Errorf("RoundTripMirror calls = %d, want 0", got)
	}
}

// TestHandler_MirrorFanout_CacheMissNoFanout pins that a
// cache-miss (LookupMirrorRules returns nil, false) spawns
// zero goroutines. Cache-miss must not block the customer
// request on a store read.
func TestHandler_MirrorFanout_CacheMissNoFanout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("source"))
	}))
	t.Cleanup(upstream.Close)

	b := &mirrorFakeBackend{
		app: App{
			ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro,
			PublicAuth: PublicAuthConfig{Mode: api.AppPublicAuthModeOpen},
		},
		host:         "jane-api.apps.dom",
		upstreamAddr: upstream.Listener.Addr().String(),
		// mirrorRules empty → LookupMirrorRules returns (nil, false).
	}
	rt := &stubMirrorRoundTripper{
		cannedResponse: &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(stringBody("unused")),
		},
	}

	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.WithMirrorRoundTripper(rt)

	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	time.Sleep(50 * time.Millisecond)
	if got := b.scheduleCalls.Load(); got != 0 {
		t.Errorf("ScheduleMirror calls = %d, want 0 (cache-miss must not fan out)", got)
	}
}

// waitForRTCalls polls until the round-tripper count reaches at
// least n or the deadline expires. Symmetric with
// waitForMirrorCalls.
func waitForRTCalls(t *testing.T, rt *stubMirrorRoundTripper, n int32, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if atomic.LoadInt32(&rt.calls) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("RoundTripMirror calls = %d, want ≥%d within %v", atomic.LoadInt32(&rt.calls), n, deadline)
}

// stringBody is a tiny helper to satisfy io.ReadCloser in the
// canned mirror response without importing strings/bytes for
// the single call-site.
type stringReadCloser string

func (s stringReadCloser) Read(p []byte) (int, error) {
	n := copy(p, []byte(s))
	if n < len(s) {
		return n, nil
	}
	return n, io.EOF
}
func (s stringReadCloser) Close() error { return nil }

// stringBody adapts a string into an io.ReadCloser for the
// canned mirror response body. Mirrors the same shape the
// production http.Response uses.
func stringBody(s string) io.ReadCloser { return stringReadCloser(s) }
