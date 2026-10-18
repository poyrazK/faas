// spec: §11
// adr: 093
package middleware_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/middleware"
)

func TestAuthLimitCountsCommittedFinalStatus(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, tc := range []struct {
			hints bool
			flush bool
		}{{true, false}, {false, true}, {true, true}} {
			t.Run(fmt.Sprintf("h2=%v/hints=%v/flush=%v", h2, tc.hints, tc.flush), func(t *testing.T) {
				cfg := middleware.AuthLimitConfig{MaxFailures: 1, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
				handler := middleware.AuthLimit(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if tc.hints {
						w.WriteHeader(http.StatusEarlyHints)
						w.WriteHeader(http.StatusEarlyHints)
					}
					if tc.flush {
						// Flush commits 200. A later error cannot change the wire
						// status and must not consume the authentication bucket.
						w.(http.Flusher).Flush()
					}
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = io.WriteString(w, "body")
				}))
				done := make(chan struct{}, 2)
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					handler.ServeHTTP(w, r)
					done <- struct{}{}
				}))
				server.EnableHTTP2 = h2
				server.StartTLS()
				t.Cleanup(server.Close)
				want := []int{http.StatusUnauthorized, http.StatusTooManyRequests}
				if tc.flush {
					want = []int{http.StatusOK, http.StatusOK}
				}
				for i, status := range want {
					resp, err := server.Client().Get(server.URL)
					if err != nil {
						t.Fatal(err)
					}
					_, err = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					if err != nil {
						t.Fatal(err)
					}
					<-done // The limiter updates the bucket after the handler returns.
					if resp.StatusCode != status {
						t.Errorf("request %d: status=%d, want %d", i+1, resp.StatusCode, status)
					}
				}
			})
		}
	}
}

type authLimitDeadlineWriter struct {
	*httptest.ResponseRecorder
	readDeadline  time.Time
	writeDeadline time.Time
}

func (w *authLimitDeadlineWriter) SetReadDeadline(deadline time.Time) error {
	w.readDeadline = deadline
	return nil
}

func (w *authLimitDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.writeDeadline = deadline
	return nil
}

func TestAuthLimitPreservesStreamAndUploadDeadlines(t *testing.T) {
	want := time.Now().Add(time.Minute)
	w := &authLimitDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	handler := middleware.AuthLimit(middleware.AuthLimitConfig{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		controller := http.NewResponseController(w)
		if err := controller.SetReadDeadline(want); err != nil {
			t.Errorf("set upload read deadline: %v", err)
		}
		if err := controller.SetWriteDeadline(want); err != nil {
			t.Errorf("set stream write deadline: %v", err)
		}
	}))
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events", nil))
	if !w.readDeadline.Equal(want) || !w.writeDeadline.Equal(want) {
		t.Fatalf("read deadline=%v, write deadline=%v; want %v", w.readDeadline, w.writeDeadline, want)
	}
}
