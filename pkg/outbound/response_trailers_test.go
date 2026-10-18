// adr: 294
package outbound

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHandlerPreservesProviderTrailers(t *testing.T) {
	for _, tc := range []struct {
		name          string
		http2         bool
		cacheTTL      int
		routed        bool
		cookie        bool
		upstreamHTTP2 bool
		knownLength   bool
	}{
		{name: "HTTP1"},
		{name: "HTTP1 cached", cacheTTL: 60},
		{name: "HTTP2", http2: true},
		{name: "HTTP2 cached", http2: true, cacheTTL: 60},
		{name: "HTTP1 routed cached", cacheTTL: 60, routed: true},
		{name: "HTTP2 routed cached", http2: true, cacheTTL: 60, routed: true},
		{name: "HTTP1 cookie trailer", cacheTTL: 60, cookie: true},
		{name: "HTTP2 cookie trailer", http2: true, cacheTTL: 60, cookie: true},
		{name: "HTTP2 length to HTTP1", upstreamHTTP2: true, knownLength: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if (tc.http2 || tc.upstreamHTTP2) && r.ProtoMajor != 2 {
					t.Errorf("provider protocol = %s, want HTTP/2", r.Proto)
				}
				version := calls.Add(1)
				body := fmt.Sprintf("provider body %d", version)
				w.Header().Set("Cache-Control", "public, max-age=60")
				w.Header().Set("Trailer", "X-Body-SHA256")
				if tc.knownLength {
					w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				}
				if tc.cookie {
					w.Header().Add("Trailer", "Set-Cookie")
				}
				_, _ = io.WriteString(w, body)
				w.Header().Set("X-Body-SHA256", fmt.Sprintf("%x", sha256.Sum256([]byte(body))))
				w.Header().Set(http.TrailerPrefix+"X-Final-Version", strconv.Itoa(int(version)))
				if tc.cookie {
					w.Header().Set("Set-Cookie", fmt.Sprintf("session=version-%d", version))
				}
			}))
			provider.EnableHTTP2 = tc.http2 || tc.upstreamHTTP2
			provider.StartTLS()
			t.Cleanup(provider.Close)
			integration := testIntegration(t, provider.URL, "gateway-token", []string{"app-1"}, 100, 10, 10)
			integration.ResponseCacheTTLSeconds = tc.cacheTTL
			resolver, err := NewStaticResolver([]Integration{integration})
			if err != nil {
				t.Fatal(err)
			}
			h, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
			if err != nil {
				t.Fatal(err)
			}
			gateway := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.routed {
					w = newRouteAttemptWriter(w, integration.ID, false)
				}
				h.ServeHTTP(w, r)
			}))
			gateway.EnableHTTP2 = tc.http2
			gateway.StartTLS()
			t.Cleanup(gateway.Close)
			for attempt := 1; attempt <= 2; attempt++ {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, gateway.URL+Prefix+integration.ID+"/stream", nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set(TokenHeader, "gateway-token")
				req.Header.Set(AppHeader, "app-1")
				resp, err := gateway.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if readErr != nil || resp.StatusCode != http.StatusOK {
					t.Fatalf("provider response = %d %q, %v", resp.StatusCode, body, readErr)
				}
				if tc.http2 && resp.ProtoMajor != 2 {
					t.Errorf("gateway protocol = %s, want HTTP/2", resp.Proto)
				}
				if got, want := resp.Trailer.Get("X-Body-SHA256"), fmt.Sprintf("%x", sha256.Sum256(body)); got != want {
					t.Errorf("attempt %d checksum trailer = %q, want %q", attempt, got, want)
				}
				version := resp.Trailer.Get("X-Final-Version")
				if version == "" || string(body) != "provider body "+version {
					t.Errorf("attempt %d final trailer = %q for body %q", attempt, version, body)
				}
				if resp.Header.Get("X-Body-SHA256") != "" || resp.Header.Get("X-Final-Version") != "" {
					t.Error("trailers were promoted to initial headers")
				}
				if tc.cookie && resp.Trailer.Get("Set-Cookie") != "session=version-"+version {
					t.Errorf("cookie trailer = %q for version %s", resp.Trailer.Get("Set-Cookie"), version)
				}
			}
			wantCalls := int32(2)
			if tc.cacheTTL > 0 && !tc.cookie {
				wantCalls = 1
			}
			if calls.Load() != wantCalls {
				t.Errorf("provider calls = %d, want %d", calls.Load(), wantCalls)
			}
		})
	}
}

func TestHandlerRejectsOversizedProviderTrailers(t *testing.T) {
	for _, cacheTTL := range []int{0, 60} {
		t.Run(fmt.Sprintf("cache=%d", cacheTTL), func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Cache-Control", "public, max-age=60")
				w.Header().Set("Trailer", "X-Large")
				_, _ = io.WriteString(w, "body")
				w.Header().Set("X-Large", strings.Repeat("x", 2048))
			}))
			t.Cleanup(provider.Close)
			integration := testIntegration(t, provider.URL, "gateway-token", []string{"app-1"}, 100, 10, 10)
			integration.ResponseCacheTTLSeconds = cacheTTL
			resolver, err := NewStaticResolver([]Integration{integration})
			if err != nil {
				t.Fatal(err)
			}
			h, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
			if err != nil {
				t.Fatal(err)
			}
			h.MaxResponseHeaderBytes = 1024
			gateway := httptest.NewServer(h)
			t.Cleanup(gateway.Close)
			for range 2 {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, gateway.URL+Prefix+integration.ID+"/stream", nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set(TokenHeader, "gateway-token")
				req.Header.Set(AppHeader, "app-1")
				resp, err := gateway.Client().Do(req)
				if err != nil {
					if cacheTTL > 0 {
						t.Errorf("buffered response should return a gateway error: %v", err)
					}
					continue
				}
				_, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if cacheTTL > 0 {
					if resp.StatusCode != http.StatusBadGateway || readErr != nil {
						t.Errorf("buffered response = %d, %v; want 502", resp.StatusCode, readErr)
					}
				} else if readErr == nil {
					t.Error("oversized streamed trailers ended as a complete response")
				}
			}
			if calls.Load() != 2 {
				t.Errorf("invalid provider metadata was cached (%d provider calls)", calls.Load())
			}
		})
	}
}
