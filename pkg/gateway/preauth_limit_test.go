package gateway

// adr: 040
// The existing app and account rate-limit ceilings bound this opt-in
// per-source guard, including its pre-wake 429 behavior.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type countingConsumerAuthStore struct {
	*fakeConsumerAuthStore
	lookups atomic.Int32
}

type sharedPreAuthCentral struct {
	mu     sync.Mutex
	tokens map[string]int
	keys   []string
	err    error
}

func (b *sharedPreAuthCentral) ConsumeToken(_ context.Context, scope, subjectID, plan string, _, burst float64) (int, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return 0, false, b.err
	}
	key := scope + "/" + subjectID + "/" + plan
	b.keys = append(b.keys, key)
	if b.tokens == nil {
		b.tokens = make(map[string]int)
	}
	remaining, found := b.tokens[key]
	if !found {
		remaining = int(burst)
	}
	if remaining == 0 {
		return 0, false, nil
	}
	remaining--
	b.tokens[key] = remaining
	return remaining, true, nil
}

func (*sharedPreAuthCentral) PeekToken(context.Context, string, string, string) (int, error) {
	return 0, nil
}
func (*sharedPreAuthCentral) Invalidate(string, string, string) {}

func TestPreAuthCentralRouteSharesBudgetAcrossReplicas(t *testing.T) {
	shared := &sharedPreAuthCentral{}
	newReplica := func() (*Handler, *fakeBackend) {
		h, b, _ := newTestHandler(t)
		b.app.PreAuthRateLimit = &api.PreAuthRateLimitConfig{
			Mode: api.PreAuthRateLimitEnforce, RequestsPerSecond: 10, Burst: 10,
			Routes: []api.PreAuthRouteLimit{{Method: "POST", Path: "/login", RequestsPerSecond: 1, Burst: 1,
				Coordination: api.PreAuthCoordinationCentral}},
		}
		h.preAuthLimiter.now = func() time.Time { return time.Unix(100, 0) }
		h.WithPreAuthCentralBackend(shared)
		return h, b
	}
	first, _ := newReplica()
	second, secondBackend := newReplica()
	request := func(h *Handler, method, source string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://jane-api.apps.dom/login", nil)
		req.Header.Set("X-Forwarded-For", source)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := request(first, "POST", "192.0.2.1"); rec.Code != http.StatusOK {
		t.Fatalf("first replica = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request(second, "POST", "192.0.2.1"); rec.Code != http.StatusTooManyRequests ||
		rec.Header().Get("x-faas-rate-limit-scope") != "pre-auth-route" {
		t.Fatalf("second replica = %d headers=%v: %s", rec.Code, rec.Header(), rec.Body.String())
	}
	if got := atomic.LoadInt32(secondBackend.Admits()); got != 0 {
		t.Fatalf("shared denial reached wake: %d", got)
	}
	if rec := request(second, "POST", "192.0.2.2"); rec.Code != http.StatusOK {
		t.Fatalf("other source = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request(second, "GET", "192.0.2.1"); rec.Code != http.StatusOK {
		t.Fatalf("other method = %d: %s", rec.Code, rec.Body.String())
	}
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if len(shared.keys) != 3 || shared.keys[0] != shared.keys[1] || shared.keys[2] == shared.keys[0] {
		t.Fatalf("shared keys = %v", shared.keys)
	}
	if strings.Contains(strings.Join(shared.keys, " "), "192.0.2") {
		t.Fatalf("central key persisted raw source: %v", shared.keys)
	}
}

func TestPreAuthCentralRouteFallsBackLocallyOnError(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.setLegacyHot()
	b.app.PreAuthRateLimit = &api.PreAuthRateLimitConfig{
		Mode: api.PreAuthRateLimitEnforce, RequestsPerSecond: 10, Burst: 10,
		Routes: []api.PreAuthRouteLimit{{Method: "POST", Path: "/login", RequestsPerSecond: 1, Burst: 1,
			Coordination: api.PreAuthCoordinationCentral}},
	}
	h.preAuthLimiter.now = func() time.Time { return time.Unix(100, 0) }
	h.WithPreAuthCentralBackend(&sharedPreAuthCentral{err: fmt.Errorf("database down")})
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://jane-api.apps.dom/login", nil)
		req.Header.Set("X-Forwarded-For", "192.0.2.1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := request(); rec.Code != http.StatusOK {
		t.Fatalf("local fallback first = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request(); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("local fallback second = %d: %s", rec.Code, rec.Body.String())
	}
	if got := testutil.ToFloat64(h.metrics.rateLimitDegraded.WithLabelValues("preauth")); got != 2 {
		t.Fatalf("degraded metric = %v, want 2", got)
	}
}

func TestPreAuthCentralRouteObserveRecordsSharedWouldBlock(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.setLegacyHot()
	b.app.PreAuthRateLimit = &api.PreAuthRateLimitConfig{
		Mode: api.PreAuthRateLimitObserve, RequestsPerSecond: 10, Burst: 10,
		Routes: []api.PreAuthRouteLimit{{Method: "POST", Path: "/login", RequestsPerSecond: 1, Burst: 1,
			Coordination: api.PreAuthCoordinationCentral}},
	}
	h.preAuthLimiter.now = func() time.Time { return time.Unix(100, 0) }
	h.WithPreAuthCentralBackend(&sharedPreAuthCentral{tokens: map[string]int{
		rateLimitScopePreAuth + "/" + dimensionalCentralSubjectID("app-1\x00POST /login", "source_ip", "192.0.2.1", preAuthCentralShards) + "/" + string(b.app.Plan): 0,
	}})
	req := httptest.NewRequest("POST", "http://jane-api.apps.dom/login", nil)
	req.Header.Set("X-Forwarded-For", "192.0.2.1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("observe mode rejected shared denial: %d: %s", rec.Code, rec.Body.String())
	}
	if got := testutil.ToFloat64(h.metrics.preAuthPolicyShadow.WithLabelValues("app-1", "route_0", "would_block")); got != 1 {
		t.Fatalf("route would_block = %v, want 1", got)
	}
	if got := testutil.ToFloat64(h.metrics.preAuthPolicyShadow.WithLabelValues("app-1", "route_0", "result_2xx")); got != 1 {
		t.Fatalf("route result_2xx = %v, want 1", got)
	}
}

func (s *countingConsumerAuthStore) ConsumerKeyByAppAndPrefix(ctx context.Context, accountID, appID, prefix string) (ConsumerAuthKey, error) {
	s.lookups.Add(1)
	return s.fakeConsumerAuthStore.ConsumerKeyByAppAndPrefix(ctx, accountID, appID, prefix)
}

func TestPreAuthRateLimitBlocksBeforeCredentialLookupAndWake(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.app.ConsumerAuthMode = api.ConsumerAuthModeRequired
	b.app.PreAuthRateLimit = &api.PreAuthRateLimitConfig{Mode: api.PreAuthRateLimitEnforce, RequestsPerSecond: 1, Burst: 1}
	store, token := consumerAuthFixture()
	counted := &countingConsumerAuthStore{fakeConsumerAuthStore: store}
	h.WithConsumerAuth(counted)
	fixed := time.Now()
	h.preAuthLimiter.now = func() time.Time { return fixed }

	request := func(source string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Forwarded-For", source)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := request("192.0.2.1"); rec.Code != http.StatusOK {
		t.Fatalf("first request = %d: %s", rec.Code, rec.Body.String())
	}
	if got := counted.lookups.Load(); got != 1 {
		t.Fatalf("credential lookups = %d, want 1", got)
	}
	if got := atomic.LoadInt32(b.Admits()); got != 1 {
		t.Fatalf("first request wakes = %d, want 1", got)
	}
	b.mu.Lock()
	b.running = false
	b.targets = nil
	b.mu.Unlock()

	rec := request("192.0.2.1")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "1" || rec.Header().Get("x-faas-rate-limit-scope") != "pre-auth" {
		t.Fatalf("over-limit response = %d headers=%v body=%s", rec.Code, rec.Header(), rec.Body.String())
	}
	if got := counted.lookups.Load(); got != 1 {
		t.Fatalf("over-limit request reached credential lookup: %d", got)
	}
	if got := atomic.LoadInt32(b.Admits()); got != 1 {
		t.Fatalf("over-limit request woke VM: %d", got)
	}
	if got := testutil.ToFloat64(h.metrics.preAuthRateLimited.WithLabelValues("app-1", "blocked")); got != 1 {
		t.Fatalf("blocked metric = %v, want 1", got)
	}

	if rec := request("192.0.2.2"); rec.Code != http.StatusOK {
		t.Fatalf("independent source = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPreAuthRateLimitObserveAndUntrustedSource(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.setLegacyHot()
	b.app.PreAuthRateLimit = &api.PreAuthRateLimitConfig{Mode: api.PreAuthRateLimitObserve, RequestsPerSecond: 1, Burst: 1}
	fixed := time.Now()
	h.preAuthLimiter.now = func() time.Time { return fixed }
	request := func(xff string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := request("192.0.2.1"); rec.Code != http.StatusOK {
		t.Fatalf("first observe request = %d", rec.Code)
	}
	if rec := request("192.0.2.1"); rec.Code != http.StatusOK {
		t.Fatalf("would-block request = %d", rec.Code)
	}
	if got := testutil.ToFloat64(h.metrics.preAuthRateLimited.WithLabelValues("app-1", "would_block")); got != 1 {
		t.Fatalf("would-block metric = %v, want 1", got)
	}
	if got := testutil.ToFloat64(h.metrics.preAuthPolicyShadow.WithLabelValues("app-1", "app", "result_2xx")); got != 1 {
		t.Fatalf("app shadow result_2xx = %v, want 1", got)
	}
	for _, xff := range []string{"", "192.0.2.1, 192.0.2.2", "garbage"} {
		if rec := request(xff); rec.Code != http.StatusOK {
			t.Fatalf("observe mode blocked untrusted XFF %q: %d", xff, rec.Code)
		}
	}
	b.app.PreAuthRateLimit.Mode = api.PreAuthRateLimitEnforce
	for _, xff := range []string{"", "192.0.2.1, 192.0.2.2", "garbage"} {
		if rec := request(xff); rec.Code != http.StatusForbidden {
			t.Fatalf("enforce mode accepted untrusted XFF %q: %d", xff, rec.Code)
		}
	}
	b.app.PreAuthRateLimit = nil
	if rec := request(""); rec.Code != http.StatusOK {
		t.Fatalf("disabled guard changed legacy path: %d", rec.Code)
	}
}

func TestPreAuthRateLimitBoundsSourcesAndPreservesDebt(t *testing.T) {
	l := newPreAuthSourceLimiter()
	fixed := time.Unix(100, 0)
	l.now = func() time.Time { return fixed }
	for i := 0; i < preAuthSourcesPerApp; i++ {
		if !l.Allow("app-1", fmt.Sprintf("2001:db8::%x", i), 1, 1) {
			t.Fatalf("source %d unexpectedly rejected", i)
		}
	}
	if got := len(l.apps["app-1"].sources); got != preAuthSourcesPerApp {
		t.Fatalf("source buckets = %d, want %d", got, preAuthSourcesPerApp)
	}
	if !l.Allow("app-1", "overflow-1", 1, 1) || l.Allow("app-1", "overflow-2", 1, 1) {
		t.Fatal("new sources did not share the overflow bucket")
	}
	if got := len(l.apps["app-1"].sources); got != preAuthSourcesPerApp {
		t.Fatalf("overflow grew source map to %d", got)
	}
	fixed = fixed.Add(time.Second)
	if !l.Allow("app-1", "overflow-2", 1, 1) {
		t.Fatal("refilled old source should be evictable")
	}
	if got := len(l.apps["app-1"].sources); got != preAuthSourcesPerApp {
		t.Fatalf("eviction changed bucket bound to %d", got)
	}
}

func TestPreAuthRateLimitGlobalBucketCap(t *testing.T) {
	l := newPreAuthSourceLimiter()
	l.maxSources = 2
	fixed := time.Unix(100, 0)
	l.now = func() time.Time { return fixed }
	if !l.Allow("app-1", "192.0.2.1", 1, 1) || !l.Allow("app-2", "192.0.2.2", 1, 1) {
		t.Fatal("initial source buckets were denied")
	}
	if !l.Allow("app-3", "192.0.2.3", 1, 1) || l.Allow("app-3", "192.0.2.4", 1, 1) {
		t.Fatal("global cap did not route new sources into app-3 overflow")
	}
	if l.sourceCount != 2 {
		t.Fatalf("global bucket count = %d, want 2", l.sourceCount)
	}
	fixed = fixed.Add(time.Second)
	if !l.Allow("app-3", "192.0.2.4", 1, 1) || l.sourceCount != 2 {
		t.Fatal("refilled global bucket was not safely evicted")
	}
}

func TestPreAuthRateLimitClampsAfterPlanDowngrade(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.setLegacyHot()
	b.app.Plan = api.PlanFree
	b.app.PreAuthRateLimit = &api.PreAuthRateLimitConfig{Mode: api.PreAuthRateLimitEnforce, RequestsPerSecond: 100, Burst: 500}
	fixed := time.Now()
	h.preAuthLimiter.now = func() time.Time { return fixed }
	for i := 0; i < 21; i++ {
		req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
		req.Header.Set("X-Forwarded-For", "192.0.2.1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 20 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("request %d = %d, want %d: %s", i+1, rec.Code, want, rec.Body.String())
		}
		if i == 20 && rec.Header().Get("x-faas-rate-limit-scope") != "pre-auth" {
			t.Fatalf("downgraded app used %q scope, want pre-auth", rec.Header().Get("x-faas-rate-limit-scope"))
		}
	}
}

func TestPreAuthRouteLimitBlocksSensitivePathBeforeAuthAndWake(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.app.ConsumerAuthMode = api.ConsumerAuthModeRequired
	b.app.PreAuthRateLimit = &api.PreAuthRateLimitConfig{
		Mode: api.PreAuthRateLimitEnforce, RequestsPerSecond: 10, Burst: 10,
		Routes: []api.PreAuthRouteLimit{{Method: "POST", Path: "/login", RequestsPerSecond: 1, Burst: 1}},
	}
	store, token := consumerAuthFixture()
	counted := &countingConsumerAuthStore{fakeConsumerAuthStore: store}
	h.WithConsumerAuth(counted)
	fixed := time.Now()
	h.preAuthLimiter.now = func() time.Time { return fixed }
	request := func(method, path, source string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://jane-api.apps.dom"+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Forwarded-For", source)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := request("POST", "/login", "192.0.2.1"); rec.Code != http.StatusOK {
		t.Fatalf("first login = %d: %s", rec.Code, rec.Body.String())
	}
	b.mu.Lock()
	b.running = false
	b.targets = nil
	b.mu.Unlock()
	if rec := request("POST", "/login", "192.0.2.1"); rec.Code != http.StatusTooManyRequests || rec.Header().Get("x-faas-rate-limit-scope") != "pre-auth-route" {
		t.Fatalf("second login = %d headers=%v: %s", rec.Code, rec.Header(), rec.Body.String())
	}
	if rec := request("POST", "/a/../login", "192.0.2.1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("normalized path bypassed route limit: %d", rec.Code)
	}
	if got := counted.lookups.Load(); got != 1 {
		t.Fatalf("route-limited requests reached auth lookup: %d", got)
	}
	if got := atomic.LoadInt32(b.Admits()); got != 1 {
		t.Fatalf("route-limited requests woke VM: %d", got)
	}
	if rec := request("GET", "/login", "192.0.2.1"); rec.Code != http.StatusOK {
		t.Fatalf("other method = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request("POST", "/other", "192.0.2.1"); rec.Code != http.StatusOK {
		t.Fatalf("other path = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request("POST", "/login", "192.0.2.2"); rec.Code != http.StatusOK {
		t.Fatalf("other source = %d: %s", rec.Code, rec.Body.String())
	}
	if got := testutil.ToFloat64(h.metrics.preAuthRateLimited.WithLabelValues("app-1", "route_blocked")); got != 2 {
		t.Fatalf("route blocked metric = %v, want 2", got)
	}
}

func TestPreAuthRouteLimitObserve(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.setLegacyHot()
	b.app.PreAuthRateLimit = &api.PreAuthRateLimitConfig{
		Mode: api.PreAuthRateLimitObserve, RequestsPerSecond: 10, Burst: 10,
		Routes: []api.PreAuthRouteLimit{{Method: "POST", Path: "/login", RequestsPerSecond: 1, Burst: 1}},
	}
	fixed := time.Now()
	h.preAuthLimiter.now = func() time.Time { return fixed }
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "http://jane-api.apps.dom/login", nil)
		req.Header.Set("X-Forwarded-For", "192.0.2.1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("observe request %d = %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	if got := testutil.ToFloat64(h.metrics.preAuthRateLimited.WithLabelValues("app-1", "route_would_block")); got != 1 {
		t.Fatalf("route observe metric = %v, want 1", got)
	}
	policy := "route_0"
	for _, outcome := range []string{"would_block", "result_2xx"} {
		if got := testutil.ToFloat64(h.metrics.preAuthPolicyShadow.WithLabelValues("app-1", policy, outcome)); got != 1 {
			t.Fatalf("route shadow %s = %v, want 1", outcome, got)
		}
	}
}

func TestPreAuthShadowCountsAllPoliciesAndGatewayResponse(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.app.ConsumerAuthMode = api.ConsumerAuthModeRequired
	b.app.PreAuthRateLimit = &api.PreAuthRateLimitConfig{
		Mode: api.PreAuthRateLimitObserve, RequestsPerSecond: 1, Burst: 1,
		Routes: []api.PreAuthRouteLimit{{Method: "POST", Path: "/login", RequestsPerSecond: 1, Burst: 1}},
	}
	h.preAuthLimiter.now = func() time.Time { return time.Unix(100, 0) }
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "http://jane-api.apps.dom/login", nil)
		req.Header.Set("X-Forwarded-For", "192.0.2.1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("gateway auth response %d = %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	for _, policy := range []string{"app", "route_0"} {
		for _, outcome := range []string{"would_block", "result_4xx"} {
			if got := testutil.ToFloat64(h.metrics.preAuthPolicyShadow.WithLabelValues("app-1", policy, outcome)); got != 1 {
				t.Fatalf("%s %s = %v, want 1", policy, outcome, got)
			}
		}
	}
}

func TestPreAuthFailedResponsesBlockOnlyAfterApplicationFailures(t *testing.T) {
	h, b, _ := newTestHandler(t)
	var originStatus atomic.Int32
	var originCalls atomic.Int32
	originStatus.Store(http.StatusOK)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		originCalls.Add(1)
		w.WriteHeader(int(originStatus.Load()))
	}))
	t.Cleanup(upstream.Close)
	b.upstream = upstream.Listener.Addr().String()
	b.setLegacyHot()
	b.app.PreAuthRateLimit = &api.PreAuthRateLimitConfig{
		Mode: api.PreAuthRateLimitEnforce, RequestsPerSecond: 100, Burst: 100,
		Routes: []api.PreAuthRouteLimit{{
			Method: "POST", Path: "/login", RequestsPerSecond: 100, Burst: 100,
			FailedResponses: &api.PreAuthFailedResponseLimit{FailuresPerMinute: 5, Burst: 2},
		}},
	}
	fixed := time.Now()
	h.preAuthLimiter.now = func() time.Time { return fixed }
	request := func(source string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://jane-api.apps.dom/login", nil)
		req.Header.Set("X-Forwarded-For", source)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for i := 0; i < 3; i++ {
		if rec := request("192.0.2.1"); rec.Code != http.StatusOK {
			t.Fatalf("successful request %d = %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	originStatus.Store(http.StatusUnauthorized)
	for i := 0; i < 2; i++ {
		if rec := request("192.0.2.1"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failed response %d = %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	b.mu.Lock()
	b.running = false
	b.targets = nil
	b.mu.Unlock()
	if rec := request("192.0.2.1"); rec.Code != http.StatusTooManyRequests ||
		rec.Header().Get("x-faas-rate-limit-scope") != "pre-auth-failures" || rec.Header().Get("Retry-After") != "12" {
		t.Fatalf("failed-response limit = %d headers=%v: %s", rec.Code, rec.Header(), rec.Body.String())
	}
	if got := originCalls.Load(); got != 5 {
		t.Fatalf("blocked request reached origin: %d calls", got)
	}
	if got := atomic.LoadInt32(b.Admits()); got != 0 {
		t.Fatalf("blocked request woke app: %d admits", got)
	}
	if rec := request("192.0.2.2"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("independent source = %d: %s", rec.Code, rec.Body.String())
	}
	originStatus.Store(http.StatusOK)
	fixed = fixed.Add(12 * time.Second)
	if rec := request("192.0.2.1"); rec.Code != http.StatusOK {
		t.Fatalf("refilled failure budget = %d: %s", rec.Code, rec.Body.String())
	}
	if got := testutil.ToFloat64(h.metrics.preAuthRateLimited.WithLabelValues("app-1", "failure_recorded")); got != 3 {
		t.Fatalf("recorded failures = %v, want 3", got)
	}
}

func TestPreAuthFailedResponsesChargeConcurrentFailures(t *testing.T) {
	l := newPreAuthSourceLimiter()
	fixed := time.Unix(100, 0)
	l.now = func() time.Time { return fixed }
	const rate = 5.0 / 60
	for i := 0; i < 2; i++ {
		if available, _ := l.AvailableRate("app-1\x00POST /login\x00failures", "192.0.2.1", rate, 1); !available {
			t.Fatalf("concurrent request %d should have entered before failures completed", i)
		}
	}
	for i := 0; i < 2; i++ {
		l.RecordRate("app-1\x00POST /login\x00failures", "192.0.2.1", rate, 1)
	}
	if available, retryAfter := l.AvailableRate("app-1\x00POST /login\x00failures", "192.0.2.1", rate, 1); available || retryAfter != 24 {
		t.Fatalf("two failures should require 24 seconds of refill: available=%t retry_after=%d", available, retryAfter)
	}
	fixed = fixed.Add(24 * time.Second)
	if available, _ := l.AvailableRate("app-1\x00POST /login\x00failures", "192.0.2.1", rate, 1); !available {
		t.Fatal("failure debt did not refill")
	}
}

func TestPreAuthFailedResponsesObserveConfiguredStatus(t *testing.T) {
	h, b, _ := newTestHandler(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	t.Cleanup(upstream.Close)
	b.upstream = upstream.Listener.Addr().String()
	b.setLegacyHot()
	b.app.PreAuthRateLimit = &api.PreAuthRateLimitConfig{
		Mode: api.PreAuthRateLimitObserve, RequestsPerSecond: 100, Burst: 100,
		Routes: []api.PreAuthRouteLimit{{
			Method: "POST", Path: "/login", RequestsPerSecond: 100, Burst: 100,
			FailedResponses: &api.PreAuthFailedResponseLimit{FailuresPerMinute: 5, Burst: 1, Statuses: []int{422}},
		}},
	}
	fixed := time.Now()
	h.preAuthLimiter.now = func() time.Time { return fixed }
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "http://jane-api.apps.dom/login", nil)
		req.Header.Set("X-Forwarded-For", "192.0.2.1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("observe response %d = %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	if got := testutil.ToFloat64(h.metrics.preAuthRateLimited.WithLabelValues("app-1", "failure_would_block")); got != 1 {
		t.Fatalf("would-block metric = %v, want 1", got)
	}
	policy := "failures_0"
	for _, outcome := range []string{"would_block", "result_4xx"} {
		if got := testutil.ToFloat64(h.metrics.preAuthPolicyShadow.WithLabelValues("app-1", policy, outcome)); got != 1 {
			t.Fatalf("failure shadow %s = %v, want 1", outcome, got)
		}
	}
}

func TestPreAuthFailedResponsesIgnoreGatewayAuthDenials(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.app.ConsumerAuthMode = api.ConsumerAuthModeRequired
	b.app.PreAuthRateLimit = &api.PreAuthRateLimitConfig{
		Mode: api.PreAuthRateLimitEnforce, RequestsPerSecond: 100, Burst: 100,
		Routes: []api.PreAuthRouteLimit{{
			Method: "POST", Path: "/login", RequestsPerSecond: 100, Burst: 100,
			FailedResponses: &api.PreAuthFailedResponseLimit{FailuresPerMinute: 5, Burst: 1},
		}},
	}
	fixed := time.Now()
	h.preAuthLimiter.now = func() time.Time { return fixed }
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "http://jane-api.apps.dom/login", nil)
		req.Header.Set("X-Forwarded-For", "192.0.2.1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("gateway auth denial %d = %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	policyID := "app-1\x00POST /login\x00failures"
	if available, _ := h.preAuthLimiter.AvailableRate(policyID, "192.0.2.1", 5.0/60, 1); !available {
		t.Fatal("gateway-generated 401 spent the application failure budget")
	}
	if got := testutil.ToFloat64(h.metrics.preAuthRateLimited.WithLabelValues("app-1", "failure_recorded")); got != 0 {
		t.Fatalf("gateway auth denials recorded as application failures: %v", got)
	}
}
