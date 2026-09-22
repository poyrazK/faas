package outbound

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// docs/ops/outbound-integrations.md: preserve provider paths, queries and
// responses across the fixed-origin gateway.
func TestHandlerPreservesEscapedProviderPath(t *testing.T) {
	for _, tc := range []struct {
		name, originPath, path, want string
	}{
		{"encoded slash", "/api", "/objects/folder%2Fkey?sig=a%2Bb", "/api/objects/folder%2Fkey?sig=a%2Bb"},
		{"encoded origin", "/tenant%2Fone/", "/objects/a%2fb", "/tenant%2Fone/objects/a%2fb"},
		{"escaped percent", "", "/objects/a%252Fb", "/objects/a%252Fb"},
		{"unicode and spaces", "", "/caf%C3%A9/a%20b?q=two+words", "/caf%C3%A9/a%20b?q=two+words"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.RequestURI
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(provider.Close)
			integration := testIntegration(t, provider.URL+tc.originPath, "secret", []string{"app-1"}, 100, 1, 1)
			resolver, err := NewStaticResolver([]Integration{integration})
			if err != nil {
				t.Fatal(err)
			}
			handler, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, gatewayRequest(Prefix+integration.ID+tc.path, "secret", "app-1", http.MethodGet, nil))
			if rr.Code != http.StatusNoContent || got != tc.want {
				t.Fatalf("status=%d provider URI=%q; want 204, %q", rr.Code, got, tc.want)
			}
		})
	}
}

func TestHandlerPreservesRequestContentLength(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		length     int64
	}{
		{"known", "payload", 7},
		{"empty", "", 0},
		{"unknown", "payload", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != tc.body || r.ContentLength != tc.length {
					t.Errorf("provider body=%q length=%d err=%v; want %q, %d", body, r.ContentLength, err, tc.body, tc.length)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(provider.Close)
			integration := testIntegration(t, provider.URL, "secret", []string{"app-1"}, 100, 1, 1)
			resolver, err := NewStaticResolver([]Integration{integration})
			if err != nil {
				t.Fatal(err)
			}
			handler, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
			if err != nil {
				t.Fatal(err)
			}
			r := gatewayRequest(Prefix+integration.ID+"/upload", "secret", "app-1", http.MethodPost, strings.NewReader(tc.body))
			r.ContentLength = tc.length
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, r)
			if rr.Code != http.StatusNoContent {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHandlerAllowsLargeRepresentationMetadataWithoutBody(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		status       int
	}{
		{"HEAD", http.MethodHead, http.StatusOK},
		{"not modified", http.MethodGet, http.StatusNotModified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "100")
				w.WriteHeader(tc.status)
			}))
			t.Cleanup(provider.Close)
			integration := testIntegration(t, provider.URL, "secret", []string{"app-1"}, 100, 1, 1)
			resolver, err := NewStaticResolver([]Integration{integration})
			if err != nil {
				t.Fatal(err)
			}
			handler, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
			if err != nil {
				t.Fatal(err)
			}
			handler.MaxResponseBytes = 4
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, gatewayRequest(Prefix+integration.ID+"/large-file", "secret", "app-1", tc.method, nil))
			if rr.Code != tc.status || rr.Body.Len() != 0 {
				t.Fatalf("status=%d body=%q; want %d and no body", rr.Code, rr.Body.String(), tc.status)
			}
		})
	}
}

func TestForwardedHeadersStripCanonicalTE(t *testing.T) {
	in := make(http.Header)
	in.Set("TE", "trailers")
	in.Set("Connection", "X-Provider-Hop")
	in.Set("X-Provider-Hop", "private")
	in.Set("Authorization", "Bearer provider")
	out := forwardedHeaders(in)
	if out.Get("Te") != "" || out.Get("X-Provider-Hop") != "" || out.Get("Connection") != "" {
		t.Fatalf("hop-by-hop headers forwarded: %v", out)
	}
	if out.Get("Authorization") != in.Get("Authorization") {
		t.Fatal("provider authentication was stripped")
	}
}
