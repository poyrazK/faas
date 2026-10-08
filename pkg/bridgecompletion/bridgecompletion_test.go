package bridgecompletion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCompletionWaitsForCleanupAfterCancellation(t *testing.T) {
	started, cancelled, cleanup := make(chan struct{}), make(chan struct{}), make(chan struct{})
	id := uuid.NewString()
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(cancelled)
		<-cleanup
	}))
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/guest", nil).WithContext(ctx)
	req.Header.Set(ExchangeHeader, id)
	done := make(chan struct{})
	go func() { h.ServeHTTP(httptest.NewRecorder(), req); close(done) }()
	<-started
	cancel()
	<-cancelled
	waitReq := httptest.NewRequest(http.MethodGet, "/", nil)
	waitReq.Header.Set(WaitHeader, id)
	waitDone := make(chan struct{})
	rr := httptest.NewRecorder()
	go func() { h.ServeHTTP(rr, waitReq); close(waitDone) }()
	select {
	case <-waitDone:
		t.Fatal("acknowledged before cleanup")
	case <-time.After(20 * time.Millisecond):
	}
	close(cleanup)
	<-done
	<-waitDone
	if rr.Code != http.StatusNoContent {
		t.Fatalf("completion status %d", rr.Code)
	}
	// A successful acknowledgement consumes the retained entry.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, waitReq)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("completed entry retained: %d", rr.Code)
	}
}

func TestCompletionRefusesUnknownExchangeWithoutCallingGuest(t *testing.T) {
	h := Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("completion reached guest") }))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(WaitHeader, uuid.NewString())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d", rr.Code)
	}
}
