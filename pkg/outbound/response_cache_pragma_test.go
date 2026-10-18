// adr: 294
package outbound

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestHandlerCacheHonorsEveryPragmaField(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pragma []string
		bypass bool
	}{
		{"single directive", []string{"no-cache"}, true},
		{"later field", []string{"extension=1", "No-Cache"}, true},
		{"empty first field", []string{"", "no-cache"}, true},
		{"combined fields", []string{"extension=1, no-cache"}, true},
		{"unrelated extension", []string{"extension=1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				version := calls.Add(1)
				w.Header().Set("Cache-Control", "public, max-age=60")
				_, _ = fmt.Fprintf(w, "version %d", version)
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
			for attempt := 1; attempt <= 2; attempt++ {
				r := gatewayRequest(Prefix+integration.ID+"/v1/catalog", "gateway-token", "app-1", http.MethodGet, nil)
				r.Header["Pragma"] = append([]string(nil), tc.pragma...)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				version := 1
				if tc.bypass {
					version = attempt
				}
				if w.Code != http.StatusOK || w.Body.String() != fmt.Sprintf("version %d", version) {
					t.Errorf("attempt %d received %d %q, want version %d", attempt, w.Code, w.Body.String(), version)
				}
			}
			wantCalls := int32(1)
			if tc.bypass {
				wantCalls = 2
			}
			if calls.Load() != wantCalls {
				t.Errorf("provider calls = %d, want %d", calls.Load(), wantCalls)
			}
		})
	}
}
