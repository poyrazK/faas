// adr: 047
package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// The public listener's ReadTimeout and WriteTimeout are the customer request
// envelope. InternalReverseProxy lifted the request budget for a long-lived
// response but never moved the listener's own deadlines, so every SSE or gRPC
// stream through gatewayd-public was cut at the envelope (about 25 minutes)
// however recently it made progress. A front server with 300 ms timeouts
// stands in for the envelope here.
func TestInternalProxyStreamOutlivesListenerTimeouts(t *testing.T) {
	const chunk = "data: progress\n\n"
	const count = 12
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set(api.StreamingStatusHeader, string(api.StreamingStatusStreaming))
			w.WriteHeader(http.StatusOK)
			for i := 0; i < count; i++ {
				if _, err := io.WriteString(w, chunk); err != nil {
					return
				}
				w.(http.Flusher).Flush()
				time.Sleep(100 * time.Millisecond)
			}
		case "/slow":
			// An ordinary response keeps the envelope.
			time.Sleep(700 * time.Millisecond)
			_, _ = io.WriteString(w, "late")
		}
	}))
	defer upstream.Close()

	for _, protocol := range []struct {
		name string
		h2   bool
	}{{name: "HTTP1"}, {name: "HTTP2", h2: true}} {
		t.Run(protocol.name, func(t *testing.T) {
			dialer := &stubDialer{server: upstream}
			proxy := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
			front := httptest.NewUnstartedServer(proxy)
			front.Config.ReadTimeout = 300 * time.Millisecond
			front.Config.WriteTimeout = 300 * time.Millisecond
			front.EnableHTTP2 = protocol.h2
			if protocol.h2 {
				front.StartTLS()
			} else {
				front.Start()
			}
			defer front.Close()

			response, err := front.Client().Get(front.URL + "/stream")
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if got := strings.Count(string(body), chunk); got != count || readErr != nil {
				t.Fatalf("stream delivered %d of %d events (read error %v); the listener timeout cut it", got, count, readErr)
			}

			slow, err := front.Client().Get(front.URL + "/slow")
			if err == nil {
				late, readErr := io.ReadAll(slow.Body)
				_ = slow.Body.Close()
				if readErr == nil && string(late) == "late" {
					t.Fatal("an ordinary response outlived the listener's write timeout")
				}
			}
		})
	}
}
