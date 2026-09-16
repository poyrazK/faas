package outbound

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func testIntegration(t *testing.T, origin, token string, apps []string, rate float64, burst, maxInFlight int) Integration {
	t.Helper()
	i, err := NewIntegration("integration-1", origin, token, apps, rate, burst, maxInFlight, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func gatewayRequest(path, token, app, method string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, "http://gateway.test"+path, body)
	r.Header.Set(TokenHeader, token)
	r.Header.Set(AppHeader, app)
	return r
}

func TestMemoryBackendEnforcesRateAndReleasesConcurrency(t *testing.T) {
	backend := NewMemoryBackend()
	spec := AdmissionSpec{IntegrationID: "integration-1", RatePerSecond: 1, Burst: 1, MaxInFlight: 1, LeaseTTL: time.Minute}
	first, err := backend.Admit(context.Background(), spec)
	if err != nil || !first.Granted {
		t.Fatalf("first admission = %#v, %v", first, err)
	}
	second, err := backend.Admit(context.Background(), spec)
	if err != nil || second.Granted || second.Reason != ReasonConcurrency {
		t.Fatalf("concurrency admission = %#v, %v", second, err)
	}
	if err := backend.Release(context.Background(), spec.IntegrationID, first.LeaseID); err != nil {
		t.Fatal(err)
	}
	third, err := backend.Admit(context.Background(), spec)
	if err != nil || third.Granted || third.Reason != ReasonRate {
		t.Fatalf("rate admission = %#v, %v", third, err)
	}
}

func TestHandlersShareOneBackendAcrossInstances(t *testing.T) {
	entered := make(chan struct{})
	finish := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-finish
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	integration := testIntegration(t, server.URL, "secret", []string{"app-1"}, 100, 1, 1)
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handlerA, _ := NewHandler(resolver, backend, server.Client())
	handlerB, _ := NewHandler(resolver, backend, server.Client())

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rr := httptest.NewRecorder()
		handlerA.ServeHTTP(rr, gatewayRequest(Prefix+integration.ID+"/v1/items", "secret", "app-1", http.MethodGet, nil))
		firstDone <- rr
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first request did not reach provider")
	}

	second := httptest.NewRecorder()
	handlerB.ServeHTTP(second, gatewayRequest(Prefix+integration.ID+"/v1/items", "secret", "app-1", http.MethodGet, nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, body=%s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("Retry-After"); got == "" {
		t.Fatal("second response has no Retry-After")
	}
	if got := second.Header().Get("X-Gregale-Outbound-Rejection"); got != ReasonConcurrency {
		t.Fatalf("rejection reason = %q", got)
	}
	close(finish)
	select {
	case rr := <-firstDone:
		if rr.Code != http.StatusNoContent {
			t.Fatalf("first status = %d", rr.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("first request did not finish")
	}
}

func TestTwentyHandlersShareOneBudget(t *testing.T) {
	entered := make(chan struct{}, 5)
	finish := make(chan struct{})
	var providerCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		entered <- struct{}{}
		<-finish
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	// This models twenty independently constructed gateway handlers. They all
	// point at one shared backend, so the five-token burst is consumed once for
	// the integration rather than twenty times (once per process).
	integration := testIntegration(t, server.URL, "secret", []string{"app-1"}, .001, 5, 20)
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handlers := make([]*Handler, 20)
	for i := range handlers {
		handlers[i], err = NewHandler(resolver, backend, server.Client())
		if err != nil {
			t.Fatal(err)
		}
	}

	responses := make(chan int, len(handlers))
	var wg sync.WaitGroup
	for _, handler := range handlers {
		wg.Add(1)
		go func(h *Handler) {
			defer wg.Done()
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, gatewayRequest(Prefix+integration.ID+"/v1/items", "secret", "app-1", http.MethodGet, nil))
			responses <- rr.Code
		}(handler)
	}

	// The five admitted requests block at the provider. This prevents token
	// refill from making the assertion timing-dependent.
	for i := 0; i < 5; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatalf("provider received %d/5 admitted requests", i)
		}
	}
	close(finish)
	wg.Wait()
	close(responses)
	var granted, rejected int
	for status := range responses {
		switch status {
		case http.StatusNoContent:
			granted++
		case http.StatusTooManyRequests:
			rejected++
		default:
			t.Errorf("unexpected handler status %d", status)
		}
	}
	if providerCalls.Load() != 5 || granted != 5 || rejected != 15 {
		t.Fatalf("shared budget: provider_calls=%d granted=%d rejected=%d; want 5/5/15", providerCalls.Load(), granted, rejected)
	}
}

func TestHandlerMetricsExposeBudgetAndProviderOutcomes(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	integration := testIntegration(t, server.URL, "secret", []string{"app-1"}, .001, 1, 1)
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	handler.Metrics = metrics

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, gatewayRequest(Prefix+integration.ID+"/v1/items", "secret", "app-1", http.MethodGet, nil))
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, gatewayRequest(Prefix+integration.ID+"/v1/items", "secret", "app-1", http.MethodGet, nil))
	if first.Code != http.StatusAccepted || second.Code != http.StatusTooManyRequests {
		t.Fatalf("statuses = %d/%d; want 202/429", first.Code, second.Code)
	}

	recorder := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://metrics/metrics", nil))
	body := recorder.Body.String()
	for _, want := range []string{
		`outbound_admissions_total{integration_id="integration-1",outcome="granted"} 1`,
		`outbound_admissions_total{integration_id="integration-1",outcome="rejected"} 1`,
		`outbound_rejections_total{integration_id="integration-1",reason="rate_limit"} 1`,
		`outbound_in_flight{integration_id="integration-1"} 0`,
		`outbound_upstream_requests_total{integration_id="integration-1",outcome="2xx"} 1`,
		`outbound_upstream_latency_seconds_count{integration_id="integration-1"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q\n%s", want, body)
		}
	}
}

func TestHandlerAuthAttachmentAndTarget(t *testing.T) {
	var gotPath string
	var gotToken, gotApp string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		gotToken = r.Header.Get(TokenHeader)
		gotApp = r.Header.Get(AppHeader)
		w.Header().Set("X-Provider", "ok")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	integration := testIntegration(t, server.URL+"/api", "secret", []string{"app-1"}, 100, 1, 2)
	resolver, _ := NewStaticResolver([]Integration{integration})
	handler, _ := NewHandler(resolver, NewMemoryBackend(), server.Client())

	badAuth := httptest.NewRecorder()
	handler.ServeHTTP(badAuth, gatewayRequest(Prefix+integration.ID+"/v1", "wrong", "app-1", http.MethodGet, nil))
	if badAuth.Code != http.StatusUnauthorized {
		t.Fatalf("bad auth status = %d", badAuth.Code)
	}
	badApp := httptest.NewRecorder()
	handler.ServeHTTP(badApp, gatewayRequest(Prefix+integration.ID+"/v1", "secret", "other", http.MethodGet, nil))
	if badApp.Code != http.StatusForbidden {
		t.Fatalf("bad app status = %d", badApp.Code)
	}
	good := httptest.NewRecorder()
	goodReq := gatewayRequest(Prefix+integration.ID+"/v1/items", "secret", "app-1", http.MethodGet, nil)
	goodReq.Header.Set("Authorization", "Bearer provider-token")
	handler.ServeHTTP(good, goodReq)
	if good.Code != http.StatusOK || good.Body.String() != "ok" {
		t.Fatalf("good response = %d %q", good.Code, good.Body.String())
	}
	if gotPath != "/api/v1/items" {
		t.Fatalf("provider path = %q", gotPath)
	}
	if gotToken != "" || gotApp != "" {
		t.Fatalf("internal headers leaked: token=%q app=%q", gotToken, gotApp)
	}
	if got := forwardedHeaders(goodReq.Header).Get("Authorization"); got != "Bearer provider-token" {
		t.Fatalf("provider auth was not forwarded: %q", got)
	}
}

func TestHandlerMakesOnlyOneAttempt(t *testing.T) {
	var calls atomic.Int32
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("provider down")
	})
	client := &http.Client{Transport: transport}
	origin, _ := url.Parse("https://provider.example")
	integration := Integration{ID: "integration-1", Origin: origin, TokenHash: hashToken("secret"), AppIDs: map[string]struct{}{"app-1": {}}, RatePerSecond: 100, Burst: 1, MaxInFlight: 1, RequestTimeout: time.Second, Enabled: true}
	resolver, _ := NewStaticResolver([]Integration{integration})
	handler, _ := NewHandler(resolver, NewMemoryBackend(), client)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, gatewayRequest(Prefix+integration.ID+"/write", "secret", "app-1", http.MethodPost, strings.NewReader("body")))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", rr.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", calls.Load())
	}
}

func TestClientBuildsOptInPathAndHeaders(t *testing.T) {
	var got *http.Request
	clientHTTP := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r
		return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	client, err := NewClient("http://gateway.test", "integration-1", "secret", "app-1", clientHTTP)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(context.Background(), http.MethodGet, "/v1/items?x=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got.URL.Path != "/i/integration-1/v1/items" || got.URL.RawQuery != "x=1" {
		t.Fatalf("request URL = %s", got.URL)
	}
	if got.Header.Get(TokenHeader) != "secret" || got.Header.Get(AppHeader) != "app-1" {
		t.Fatal("client headers missing")
	}
}

func TestHandlerDoesNotFollowRedirects(t *testing.T) {
	var calls atomic.Int32
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://other.example/"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
	origin, _ := url.Parse("https://provider.example")
	integration := Integration{ID: "integration-1", Origin: origin, TokenHash: hashToken("secret"), AppIDs: map[string]struct{}{"app-1": {}}, RatePerSecond: 100, Burst: 1, MaxInFlight: 1, RequestTimeout: time.Second, Enabled: true}
	resolver, _ := NewStaticResolver([]Integration{integration})
	handler, _ := NewHandler(resolver, NewMemoryBackend(), client)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, gatewayRequest(Prefix+integration.ID+"/redirect", "secret", "app-1", http.MethodGet, nil))
	if rr.Code != http.StatusFound || calls.Load() != 1 {
		t.Fatalf("redirect status/calls = %d/%d", rr.Code, calls.Load())
	}
}

func TestMemoryBackendConcurrentAdmissionsAreAtomic(t *testing.T) {
	backend := NewMemoryBackend()
	spec := AdmissionSpec{IntegrationID: "integration-1", RatePerSecond: 1, Burst: 1, MaxInFlight: 100, LeaseTTL: time.Minute}
	var granted atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decision, err := backend.Admit(context.Background(), spec)
			if err == nil && decision.Granted {
				granted.Add(1)
			}
		}()
	}
	wg.Wait()
	if granted.Load() != 1 {
		t.Fatalf("granted = %d, want 1", granted.Load())
	}
}

func hashToken(token string) [32]byte {
	// Keep test construction aligned with NewIntegration without exposing raw
	// tokens from the package's production API.
	return sha256.Sum256([]byte(token))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
