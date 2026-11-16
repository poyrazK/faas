// spec: §4.1

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRequestIDMiddlewareGeneratesAndPropagatesID(t *testing.T) {
	h := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := requestIDFrom(r); got == "" {
			t.Fatal("requestIDFrom returned empty id")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rr.Header().Get(api.RequestIDHeader); got == "" {
		t.Fatal("generated request id header is empty")
	}
}

// A client's x-faas-request-id is never adopted, whatever its size: the
// public edge is the trust boundary and mints every id (prod 2026-11-15).
func TestRequestIDMiddlewareNeverAdoptsSuppliedID(t *testing.T) {
	for _, supplied := range []string{"supplied-1", `"../x`, strings.Repeat("x", 128), strings.Repeat("x", 256)} {
		t.Run(supplied[:min(len(supplied), 16)], func(t *testing.T) {
			var seen string
			h := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = requestIDFrom(r)
				if seen == "" || seen == supplied || r.Header.Get(api.RequestIDHeader) != seen {
					t.Errorf("request id = %q (header %q), supplied %q", seen, r.Header.Get(api.RequestIDHeader), supplied)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(api.RequestIDHeader, supplied)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if got := rr.Header().Get(api.RequestIDHeader); got != seen {
				t.Fatalf("response request id = %q, want the minted %q", got, seen)
			}
		})
	}
}
