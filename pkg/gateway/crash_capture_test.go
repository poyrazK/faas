// adr: 733
package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type crashRecordingBackend struct {
	*fakeBackend
	calls chan [2]string
}

func (b *crashRecordingBackend) RequestCrashCapture(_ context.Context, appID, instanceID string, statusCode int, route string) {
	if statusCode >= 500 {
		b.calls <- [2]string{instanceID, route}
	}
}

func TestCrashCaptureRequestedOnceFor5xx(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			_, _ = io.WriteString(w, "fine")
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)
	b := &crashRecordingBackend{
		fakeBackend: &fakeBackend{app: App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}, host: "jane-api.apps.dom", upstream: upstream.Listener.Addr().String()},
		calls:       make(chan [2]string, 4),
	}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))

	get := func(path string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom"+path, nil))
		return rec.Code
	}
	if code := get("/ok"); code != http.StatusOK {
		t.Fatalf("ok path = %d", code)
	}
	if code := get("/boom"); code != http.StatusInternalServerError {
		t.Fatalf("5xx response changed to %d", code)
	}
	select {
	case call := <-b.calls:
		if call[1] != "/boom" || call[0] == "" {
			t.Fatalf("capture request = %v, want the failing instance and route", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no capture requested for a 5xx")
	}
	get("/boom")
	select {
	case call := <-b.calls:
		t.Fatalf("second 5xx within the throttle requested again: %v", call)
	case <-time.After(200 * time.Millisecond):
	}
}
