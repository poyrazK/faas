// adr: 096
// adr: 093
package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppErrorsRecorderObservesCommittedFinalStatus(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, flush := range []bool{false, true} {
			t.Run(fmt.Sprintf("h2=%v/flush=%v", h2, flush), func(t *testing.T) {
				recorder := newAppErrorsRecorder(appErrorsRecorderConfig{Enabled: true}, nil, nil, slog.Default())
				handler := recorder.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					api.PlatformIdentity{AppID: "app-1", TenantID: "acct-1"}.ApplyGuestHeaders(r.Header)
					if flush {
						w.(http.Flusher).Flush()
					} else {
						w.WriteHeader(http.StatusEarlyHints)
						w.WriteHeader(http.StatusEarlyHints)
					}
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, "body")
				}))
				done := make(chan struct{})
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					handler.ServeHTTP(w, r)
					close(done)
				}))
				server.EnableHTTP2 = h2
				server.StartTLS()
				t.Cleanup(server.Close)
				resp, err := server.Client().Get(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if _, err := io.Copy(io.Discard, resp.Body); err != nil {
					t.Fatal(err)
				}
				<-done
				want := http.StatusServiceUnavailable
				if flush {
					want = http.StatusOK
				}
				if resp.StatusCode != want {
					t.Errorf("wire status = %d, want %d", resp.StatusCode, want)
				}
				rows := recorder.drainBatch(10)
				if flush {
					if len(rows) != 0 {
						t.Fatalf("successful stream recorded as errors: %+v", rows)
					}
				} else if len(rows) != 1 || rows[0].HTTPStatus != http.StatusServiceUnavailable {
					t.Fatalf("error rows = %+v, want one 503 response", rows)
				}
			})
		}
	}
}

type appErrorDeadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *appErrorDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestAppErrorsRecorderPreservesStreamDeadlines(t *testing.T) {
	want := time.Now().Add(time.Minute)
	w := &appErrorDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	recorder := newAppErrorsRecorder(appErrorsRecorderConfig{Enabled: true}, nil, nil, slog.Default())
	recorder.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).SetWriteDeadline(want); err != nil {
			t.Errorf("set stream deadline: %v", err)
		}
	})).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events", nil))
	if !w.deadline.Equal(want) {
		t.Fatalf("write deadline = %v, want %v", w.deadline, want)
	}
}
