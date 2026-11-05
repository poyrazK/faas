// adr: 20261025202805

package s3gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestGatewayBucketCORS pins ADR-20261025202805: a customer origin reaches a
// bucket only through that bucket's own CORS rules, on preflights that name
// the bucket through a presigned URL and on the actual requests.
func TestGatewayBucketCORS(t *testing.T) {
	handler, store, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodOptions {
			t.Fatal("a preflight reached the provider")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Etag": []string{`"etag"`}, "Content-Length": []string{"2"}}, Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
	})
	const app = "https://app.example.test"
	store.cors = []api.ObjectBucketCORSRule{{AllowedOrigins: []string{"https://*.example.test"}, AllowedMethods: []string{"GET", "PUT"}, AllowedHeaders: []string{"content-type"}, ExposeHeaders: []string{"ETag"}, MaxAgeSeconds: 600}}
	signed := presignedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/folder/a.txt", nil).URL.String()
	preflight := func(origin, method, headers, target string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodOptions, target, nil)
		r.Host = "s3.gregale.dev"
		r.Header.Set("Origin", origin)
		r.Header.Set("Access-Control-Request-Method", method)
		if headers != "" {
			r.Header.Set("Access-Control-Request-Headers", headers)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	w := preflight(app, http.MethodPut, "content-type", signed)
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != app || w.Header().Get("Access-Control-Allow-Methods") != "GET, PUT" || w.Header().Get("Access-Control-Allow-Headers") != "content-type" || w.Header().Get("Access-Control-Max-Age") != "600" {
		t.Fatalf("allowed preflight = %d %v", w.Code, w.Header())
	}
	for name, tc := range map[string]struct{ origin, method, headers, target string }{
		"other origin":     {"https://evil.test", http.MethodPut, "", signed},
		"other method":     {app, http.MethodDelete, "", signed},
		"other header":     {app, http.MethodPut, "x-custom", signed},
		"no bucket in URL": {app, http.MethodPut, "", "https://s3.gregale.dev/assets/folder/a.txt"},
		"other bucket":     {app, http.MethodPut, "", "https://s3.gregale.dev/other/folder/a.txt?" + presignedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/folder/a.txt", nil).URL.RawQuery},
	} {
		if w := preflight(tc.origin, tc.method, tc.headers, tc.target); w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("%s: preflight = %d %v", name, w.Code, w.Header())
		}
	}

	get := func(origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := presignedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets/folder/a.txt", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := get(app); w.Header().Get("Access-Control-Allow-Origin") != app || w.Header().Get("Access-Control-Expose-Headers") != "ETag" {
		t.Fatalf("allowed request = %d %v", w.Code, w.Header())
	}
	if w := get("https://evil.test"); w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("other origin's request = %d %v", w.Code, w.Header())
	}

	// Without rules, only operator origins are allowed, as before.
	store.cors = nil
	if w := preflight(app, http.MethodPut, "", signed); w.Code != http.StatusForbidden {
		t.Fatalf("preflight without rules = %d", w.Code)
	}
	if w := preflight("https://console.example.test", http.MethodPut, "", "https://s3.gregale.dev/assets/folder/a.txt"); w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "https://console.example.test" {
		t.Fatalf("operator origin preflight = %d %v", w.Code, w.Header())
	}
}
