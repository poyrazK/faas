package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type delayedWakeBackend struct {
	*fakeBackend
	delay time.Duration
}

func (b *delayedWakeBackend) Admit(ctx context.Context, app, account, scope, hint string, maxConcurrency int) (string, WakeMethod, bool, error) {
	time.Sleep(b.delay)
	return b.fakeBackend.Admit(ctx, app, account, scope, hint, maxConcurrency)
}

func TestWakeLatencyIncludesAdmissionAndRestore(t *testing.T) {
	original, backend, _ := newTestHandler(t)
	const wakeDelay = 75 * time.Millisecond
	h := NewHandlerWith(&delayedWakeBackend{fakeBackend: backend, delay: wakeDelay}, NewMetrics(), original.log)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := histogramMeanObservation(t, h.metrics.wakeLatency); got < wakeDelay {
		t.Fatalf("full wake latency = %v, excludes the %v admission/restore delay", got, wakeDelay)
	}
}
