// adr: 294 — provider credentials and forwarded headers partition responses.
package outbound

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestHandlerCacheSeparatesHeaderValuesFromCredentials(t *testing.T) {
	for _, authenticatedFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("authenticated-first=%v", authenticatedFirst), func(t *testing.T) {
			var providerCalls atomic.Int32
			provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerCalls.Add(1)
				w.Header().Set("Cache-Control", "public, max-age=60")
				w.Header().Set("Vary", "Authorization, Accept")
				body := "public response"
				if r.Header.Get("Authorization") == "Bearer example-user" {
					body = "authenticated response"
				}
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(provider.Close)
			integration := testIntegration(t, provider.URL, "gateway-token", []string{"app-1"}, 100, 10, 10)
			integration.ResponseCacheTTLSeconds = 60
			resolver, err := NewStaticResolver([]Integration{integration})
			if err != nil {
				t.Fatal(err)
			}
			h, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
			if err != nil {
				t.Fatal(err)
			}
			for _, authenticated := range []bool{authenticatedFirst, !authenticatedFirst, authenticatedFirst, !authenticatedFirst} {
				r := gatewayRequest(Prefix+integration.ID+"/v1/catalog", "gateway-token", "app-1", http.MethodGet, nil)
				want := "public response"
				if authenticated {
					r.Header.Set("Accept", "application/json")
					r.Header.Set("Authorization", "Bearer example-user")
					want = "authenticated response"
				} else {
					// An empty value is a valid header field, not a delimiter.
					// These values must not impersonate a separate Authorization
					// header while serializing the request into the cache key.
					r.Header["Accept"] = []string{"application/json", "", "Authorization", "Bearer example-user"}
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != http.StatusOK || w.Body.String() != want {
					t.Errorf("authenticated=%v received %d %q, want %q", authenticated, w.Code, w.Body.String(), want)
				}
			}
			if calls := providerCalls.Load(); calls != 2 {
				t.Errorf("provider calls = %d, want one fill per header variant", calls)
			}
		})
	}
}
