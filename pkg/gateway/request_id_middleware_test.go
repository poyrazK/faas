// spec: §4.1

package gateway

import (
	"net/http"
	"net/http/httptest"
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

func TestRequestIDMiddlewarePreservesSuppliedID(t *testing.T) {
	h := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := requestIDFrom(r); got != "supplied-1" {
			t.Fatalf("context request id = %q, want supplied-1", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(api.RequestIDHeader, "supplied-1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if got := rr.Header().Get(api.RequestIDHeader); got != "supplied-1" {
		t.Fatalf("request id header = %q, want supplied-1", got)
	}
}
