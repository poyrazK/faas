package wire

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHTTPMetricsHandlerUsesBoundedStatusClasses pins ADR-015's bounded
// request metric contract: the response status is represented by a closed
// class label rather than an arbitrary provider or application code.
func TestHTTPMetricsHandlerUsesBoundedStatusClasses(t *testing.T) {
	ops := NewOpsMetrics("test_http")
	handler := HTTPMetricsHandler(ops, "http_request", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	recorder := httptest.NewRecorder()
	ops.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `test_http_ops_total{code="4xx",op="http_request"} 1`) {
		t.Fatalf("bounded 4xx operation metric missing from:\n%s", body)
	}
}
