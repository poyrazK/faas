package bridgecompletion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
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

func serveExchange(h http.Handler, id string, retain bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/guest", nil)
	req.Header.Set(ExchangeHeader, id)
	if retain {
		req.Header.Set(RetainRequestHeader, RetainRequestValue)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func waitStatus(h http.Handler, id string) int {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(WaitHeader, id)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}

func TestRetainedExchangeIsEchoedOnlyWhenAsked(t *testing.T) {
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	id := uuid.NewString()
	if got := serveExchange(h, id, true).Header().Get(RetainedHeader); got != id {
		t.Fatalf("retained echo = %q, want %q", got, id)
	}
	if got := serveExchange(h, uuid.NewString(), false).Header().Get(RetainedHeader); got != "" {
		t.Fatalf("echo without a retain request: %q", got)
	}
	// The completed exchange answers a late wait once.
	if code := waitStatus(h, id); code != http.StatusNoContent {
		t.Fatalf("late wait status %d", code)
	}
	if code := waitStatus(h, id); code != http.StatusNotFound {
		t.Fatalf("acknowledged exchange still retained: %d", code)
	}
}

func TestUnacknowledgedCompletionsAreForgottenOldestFirst(t *testing.T) {
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	ids := make([]string, api.TrafficBridgeCompletionRetained+api.TrafficBridgeCompletionMaxEntries+1)
	for i := range ids {
		ids[i] = uuid.NewString()
		if rr := serveExchange(h, ids[i], true); rr.Code != http.StatusOK {
			t.Fatalf("exchange %d refused with %d: completions nobody waits for must not fill the tracker", i, rr.Code)
		}
	}
	if code := waitStatus(h, ids[0]); code != http.StatusNotFound {
		t.Fatalf("oldest completion still retained: %d", code)
	}
	if code := waitStatus(h, ids[len(ids)-api.TrafficBridgeCompletionRetained]); code != http.StatusNoContent {
		t.Fatalf("a completion inside the retained window was forgotten: %d", code)
	}
}

func TestOpenExchangesStayBounded(t *testing.T) {
	const limit = 128
	release := make(chan struct{})
	h := WrapLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hold" {
			<-release
		}
		w.WriteHeader(http.StatusOK)
	}), limit)
	done := make(chan struct{}, limit)
	for range limit {
		req := httptest.NewRequest(http.MethodGet, "/hold", nil)
		req.Header.Set(ExchangeHeader, uuid.NewString())
		go func() { h.ServeHTTP(httptest.NewRecorder(), req); done <- struct{}{} }()
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		id := uuid.NewString()
		rr := serveExchange(h, id, false)
		if rr.Code == http.StatusServiceUnavailable {
			// vmmd fails only this request on the echo; without it, its
			// completion wait gets 404 and it fences the whole bridge.
			if got := rr.Header().Get(RefusedHeader); got != id {
				t.Fatalf("refused exchange echoed %q, want %q", got, id)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("open exchanges were not bounded")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	for range limit {
		<-done
	}
	if rr := serveExchange(h, uuid.NewString(), false); rr.Code != http.StatusOK {
		t.Fatalf("completed exchanges still counted as open: %d", rr.Code)
	}
}

// vmmd admits at most a plan's concurrency_per_vm requests to one instance.
// A bridge that refused below that bound would fail requests admission had
// capacity for.
func TestOpenExchangeLimitCoversEveryPlansRequestBound(t *testing.T) {
	for _, plan := range api.Plans {
		if bound := plan.ConcurrencyPerVMBound(); bound > api.TrafficBridgeCompletionMaxEntries {
			t.Errorf("plan %s admits %d requests per instance, above the bridge's %d open exchanges", plan, bound, api.TrafficBridgeCompletionMaxEntries)
		}
	}
	// adr: 20261101215228 — runtime config may raise the limit to this bound.
	if api.AppRequestConcurrencyBound > api.TrafficBridgeCompletionMaxEntries {
		t.Errorf("vmmd admits up to %d requests per instance, above the bridge's %d open exchanges", api.AppRequestConcurrencyBound, api.TrafficBridgeCompletionMaxEntries)
	}
}
