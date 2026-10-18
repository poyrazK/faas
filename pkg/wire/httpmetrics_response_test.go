// adr: 015
package wire

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHTTPMetricsPreserveFinalResponseAfterEarlyHints(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
			t.Run(fmt.Sprintf("h2=%v/status=%d", h2, status), func(t *testing.T) {
				ops := NewOpsMetrics("test_interim")
				handler := HTTPMetricsHandler(ops, "http_request", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusEarlyHints)
					w.WriteHeader(http.StatusEarlyHints)
					if status != http.StatusOK {
						w.WriteHeader(status)
					}
					_, _ = io.WriteString(w, "final body")
				}))
				done := make(chan struct{})
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					handler.ServeHTTP(w, r)
					close(done)
				}))
				server.EnableHTTP2 = h2
				server.StartTLS()
				t.Cleanup(server.Close)
				var hints atomic.Int32
				trace := &httptrace.ClientTrace{Got1xxResponse: func(code int, _ textproto.MIMEHeader) error {
					if code == http.StatusEarlyHints {
						hints.Add(1)
					}
					return nil
				}}
				req, err := http.NewRequestWithContext(httptrace.WithClientTrace(t.Context(), trace), http.MethodGet, server.URL, nil)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := server.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil || string(body) != "final body" || resp.StatusCode != status {
					t.Errorf("final response = %d %q, err=%v; want %d final body", resp.StatusCode, body, err, status)
				}
				if hints.Load() != 2 {
					t.Errorf("early hints = %d, want 2", hints.Load())
				}
				<-done
				metrics := httptest.NewRecorder()
				ops.Handler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
				want := fmt.Sprintf(`test_interim_ops_total{code="%dxx",op="http_request"} 1`, status/100)
				if !strings.Contains(metrics.Body.String(), want) {
					t.Errorf("missing final status metric %s", want)
				}
			})
		}
	}
}

type failedMetricsHijacker struct {
	*httptest.ResponseRecorder
}

func (*failedMetricsHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("connection unavailable")
}

func TestHTTPMetricsFailedHijackPreservesErrorResponse(t *testing.T) {
	underlying := &failedMetricsHijacker{httptest.NewRecorder()}
	w := &httpMetricsResponseWriter{ResponseWriter: underlying, status: http.StatusOK}
	if _, _, err := w.Hijack(); err == nil {
		t.Fatal("hijack unexpectedly succeeded")
	}
	http.Error(w, "upgrade failed", http.StatusServiceUnavailable)
	if underlying.Code != http.StatusServiceUnavailable || w.status != http.StatusServiceUnavailable {
		t.Fatalf("failed upgrade response status=%d metric=%d, want 503 for both", underlying.Code, w.status)
	}
}
