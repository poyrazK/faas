package outbound

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var errInjectedCacheRead = errors.New("injected response body read failure")

func TestHandlerCachesGETByVerifiedAppAndCredential(t *testing.T) {
	var providerCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		w.Header().Set("Cache-Control", "public, max-age=120")
		w.Header().Set("Vary", "Authorization")
		_, _ = io.WriteString(w, r.Header.Get("Authorization"))
	}))
	defer server.Close()

	integration := testIntegration(t, server.URL, "gateway-token", []string{"app-1", "app-2"}, 100, 10, 10)
	integration.ResponseCacheTTLSeconds = 60
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	call := func(appID, authorization string) *httptest.ResponseRecorder {
		req := gatewayRequest(Prefix+integration.ID+"/v1/catalog", "gateway-token", appID, http.MethodGet, nil)
		req.Header.Set("Authorization", authorization)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	if got := call("app-1", "Bearer app-one"); got.Code != http.StatusOK || got.Body.String() != "Bearer app-one" {
		t.Fatalf("first response = %d %q", got.Code, got.Body.String())
	}
	if got := call("app-1", "Bearer app-one"); got.Code != http.StatusOK || got.Body.String() != "Bearer app-one" || got.Header().Get("Age") == "" {
		t.Fatalf("cached response = %d %q headers=%v", got.Code, got.Body.String(), got.Header())
	}
	if got := call("app-1", "Bearer app-two"); got.Body.String() != "Bearer app-two" {
		t.Fatalf("credential-partitioned response = %q", got.Body.String())
	}
	if got := call("app-2", "Bearer app-one"); got.Body.String() != "Bearer app-one" {
		t.Fatalf("app-partitioned response = %q", got.Body.String())
	}
	if got := providerCalls.Load(); got != 3 {
		t.Fatalf("provider requests = %d, want 3 (one cache hit)", got)
	}
}

func TestHandlerDoesNotStoreForbiddenProviderResponses(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		cacheValue string
		vary       string
		setCookie  bool
	}{
		{name: "private", status: http.StatusOK, cacheValue: "private, max-age=120"},
		{name: "no-store", status: http.StatusOK, cacheValue: "no-store, max-age=120"},
		{name: "no-cache", status: http.StatusOK, cacheValue: "no-cache, max-age=120"},
		{name: "vary star", status: http.StatusOK, cacheValue: "max-age=120", vary: "*"},
		{name: "set-cookie", status: http.StatusOK, cacheValue: "max-age=120", setCookie: true},
		{name: "provider error", status: http.StatusServiceUnavailable, cacheValue: "max-age=120"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var providerCalls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				providerCalls.Add(1)
				w.Header().Set("Cache-Control", tc.cacheValue)
				if tc.vary != "" {
					w.Header().Set("Vary", tc.vary)
				}
				if tc.setCookie {
					w.Header().Add("Set-Cookie", "session=private")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, "provider body")
			}))
			defer server.Close()

			integration := testIntegration(t, server.URL, "gateway-token", []string{"app-1"}, 100, 10, 10)
			integration.ResponseCacheTTLSeconds = 60
			resolver, err := NewStaticResolver([]Integration{integration})
			if err != nil {
				t.Fatal(err)
			}
			handler, err := NewHandler(resolver, NewMemoryBackend(), server.Client())
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, gatewayRequest(Prefix+integration.ID+"/v1/data", "gateway-token", "app-1", http.MethodGet, nil))
				if rr.Code != tc.status {
					t.Fatalf("status = %d, want %d", rr.Code, tc.status)
				}
			}
			if got := providerCalls.Load(); got != 2 {
				t.Fatalf("provider calls = %d, want 2 because response is not cacheable", got)
			}
		})
	}
}

func TestOutboundCacheRequestEligibility(t *testing.T) {
	cases := []struct {
		name   string
		method string
		body   io.Reader
		header http.Header
		want   bool
	}{
		{name: "bodyless GET", method: http.MethodGet, want: true},
		{name: "HEAD", method: http.MethodHead, want: false},
		{name: "POST", method: http.MethodPost, want: false},
		{name: "GET with body", method: http.MethodGet, body: strings.NewReader("payload"), want: false},
		{name: "range", method: http.MethodGet, header: http.Header{"Range": {"bytes=0-10"}}, want: false},
		{name: "conditional", method: http.MethodGet, header: http.Header{"If-None-Match": {`"tag"`}}, want: false},
		{name: "request no-cache", method: http.MethodGet, header: http.Header{"Cache-Control": {"no-cache"}}, want: false},
		{name: "request no-store", method: http.MethodGet, header: http.Header{"Cache-Control": {"no-store"}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "https://provider.example/v1/data", tc.body)
			for name, values := range tc.header {
				req.Header[name] = append([]string(nil), values...)
			}
			if got := outboundCacheRequestEligible(req, 60); got != tc.want {
				t.Fatalf("eligible = %v, want %v", got, tc.want)
			}
		})
	}
	if outboundCacheRequestEligible(httptest.NewRequest(http.MethodGet, "https://provider.example", nil), 0) {
		t.Fatal("zero TTL unexpectedly enabled caching")
	}
}

func TestOutboundResponseFreshnessBoundsAndProviderDirectives(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		header    http.Header
		status    int
		wantTTL   time.Duration
		wantCache bool
	}{
		{name: "policy ttl", header: http.Header{}, status: http.StatusOK, wantTTL: 60 * time.Second, wantCache: true},
		{name: "provider max age", header: http.Header{"Cache-Control": {"public, max-age=10"}}, status: http.StatusOK, wantTTL: 10 * time.Second, wantCache: true},
		{name: "policy caps provider", header: http.Header{"Cache-Control": {"public, max-age=600"}}, status: http.StatusOK, wantTTL: 60 * time.Second, wantCache: true},
		{name: "age reduces freshness", header: http.Header{"Cache-Control": {"max-age=20"}, "Age": {"5"}}, status: http.StatusOK, wantTTL: 15 * time.Second, wantCache: true},
		{name: "expires controls freshness", header: http.Header{"Expires": {now.Add(12 * time.Second).Format(http.TimeFormat)}}, status: http.StatusOK, wantTTL: 12 * time.Second, wantCache: true},
		{name: "private", header: http.Header{"Cache-Control": {"private, max-age=30"}}, status: http.StatusOK},
		{name: "no store", header: http.Header{"Cache-Control": {"no-store"}}, status: http.StatusOK},
		{name: "no cache", header: http.Header{"Cache-Control": {"no-cache"}}, status: http.StatusOK},
		{name: "vary star", header: http.Header{"Vary": {"*"}}, status: http.StatusOK},
		{name: "set cookie", header: http.Header{"Set-Cookie": {"session=private"}}, status: http.StatusOK},
		{name: "not successful", header: http.Header{"Cache-Control": {"max-age=30"}}, status: http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotTTL, _, ok := outboundResponseFreshness(&http.Response{StatusCode: tc.status, Header: tc.header}, 60*time.Second, now)
			if ok != tc.wantCache || gotTTL != tc.wantTTL {
				t.Fatalf("freshness = %s, %v; want %s, %v", gotTTL, ok, tc.wantTTL, tc.wantCache)
			}
		})
	}
}

func TestOutboundResponseCacheKeyHidesAndPartitionsCredentials(t *testing.T) {
	cache, err := newOutboundResponseCache()
	if err != nil {
		t.Fatal(err)
	}
	first := httptest.NewRequest(http.MethodGet, "https://provider.example/v1/data", nil)
	first.Header.Set("Authorization", "Bearer very-secret-value")
	second := first.Clone(first.Context())
	second.Header.Set("Authorization", "Bearer different-secret")
	firstKey := cache.key("integration", "app-1", 1, 60, first)
	if strings.Contains(firstKey, "very-secret-value") || firstKey == cache.key("integration", "app-1", 1, 60, second) {
		t.Fatal("cache key exposed or failed to partition the provider credential")
	}
	if firstKey == cache.key("integration", "app-2", 1, 60, first) || firstKey == cache.key("integration", "app-1", 2, 60, first) {
		t.Fatal("cache key did not partition by app or policy revision")
	}
}

func TestOutboundResponseCachePreservesBodyReadErrors(t *testing.T) {
	cache, err := newOutboundResponseCache()
	if err != nil {
		t.Fatal(err)
	}
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Cache-Control": {"max-age=60"}},
		ContentLength: -1,
		Body:          &failingCacheBody{},
	}
	cache.storeResponse("key", resp, 60, time.Now(), 0, 0, 1<<20)

	body, err := io.ReadAll(resp.Body)
	if !errors.Is(err, errInjectedCacheRead) || string(body) != "partial" {
		t.Fatalf("replayed body = %q, err=%v; want partial bytes and original read error", body, err)
	}
	if _, hit := cache.get("key", time.Now()); hit {
		t.Fatal("response with a body read error was cached")
	}
}

type failingCacheBody struct {
	read bool
}

func (b *failingCacheBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, io.EOF
	}
	b.read = true
	return copy(p, "partial"), errInjectedCacheRead
}

func (b *failingCacheBody) Close() error { return nil }
