// Package bridgecompletion acknowledges that a reusable bridge has finished
// an exchange. It is carried only on vmmd's protected local bridge socket.
package bridgecompletion

import (
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	ExchangeHeader = "X-Faas-Bridge-Exchange-ID"
	WaitHeader     = "X-Faas-Bridge-Wait-For-Exchange"
	// RetainRequestHeader asks the bridge to keep the exchange's completion
	// after the handler returns, so the caller may skip the wait request when
	// it reads the response to a clean end of stream. Only RetainRequestValue
	// asks; a caller that does not ask always waits.
	RetainRequestHeader = "X-Faas-Bridge-Retain-Exchange"
	RetainRequestValue  = "1"
	// RetainedHeader echoes the exchange ID on the response when the bridge
	// retains its completion. The guest never sees the exchange ID, so it
	// cannot forge the echo.
	RetainedHeader = "X-Faas-Bridge-Exchange-Retained"
)

// exchange is one tracked exchange; done closes when the handler returns.
type exchange struct {
	id   string
	done chan struct{}
}

type tracker struct {
	mu      sync.Mutex
	entries map[string]*exchange
	// open counts exchanges whose handler has not returned. Only they are
	// bounded by TrafficBridgeCompletionMaxEntries.
	open int
	// retained holds the most recent completions; the oldest is forgotten
	// when a new one arrives, so an exchange nobody waits for cannot leak.
	retained []*exchange
	next     int
}

// Wrap marks completion after the inner handler has closed its guest body
// and connection. Cancelling the inbound H2 stream is not itself completion.
// A completed exchange stays known until a wait acknowledges it or the last
// TrafficBridgeCompletionRetained completions have pushed it out.
//
// An HTTP/2 server ends a response stream only after its handler returns, so
// a caller that read a retained exchange's response to a clean end of stream
// already knows it completed and need not wait.
func Wrap(next http.Handler) http.Handler {
	t := &tracker{
		entries:  make(map[string]*exchange),
		retained: make([]*exchange, api.TrafficBridgeCompletionRetained),
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get(WaitHeader); id != "" {
			t.wait(w, r, id)
			return
		}
		id := r.Header.Get(ExchangeHeader)
		if id == "" { // legacy per-RPC bridges are fenced by process reap
			next.ServeHTTP(w, r)
			return
		}
		ex, ok := t.begin(id)
		if !ok {
			http.Error(w, "invalid bridge exchange", http.StatusServiceUnavailable)
			return
		}
		defer t.finish(ex)
		if r.Header.Get(RetainRequestHeader) == RetainRequestValue {
			w.Header().Set(RetainedHeader, id)
		}
		next.ServeHTTP(w, r)
	})
}

func (t *tracker) begin(id string) (*exchange, bool) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.entries[id]; exists || t.open >= api.TrafficBridgeCompletionMaxEntries {
		return nil, false
	}
	ex := &exchange{id: id, done: make(chan struct{})}
	t.entries[id] = ex
	t.open++
	return ex, true
}

func (t *tracker) finish(ex *exchange) {
	t.mu.Lock()
	defer t.mu.Unlock()
	close(ex.done)
	t.open--
	if oldest := t.retained[t.next]; oldest != nil && t.entries[oldest.id] == oldest {
		delete(t.entries, oldest.id)
	}
	t.retained[t.next] = ex
	t.next = (t.next + 1) % len(t.retained)
}

func (t *tracker) wait(w http.ResponseWriter, r *http.Request, id string) {
	t.mu.Lock()
	ex := t.entries[id]
	t.mu.Unlock()
	if ex == nil {
		http.Error(w, "unknown bridge exchange", http.StatusNotFound)
		return
	}
	select {
	case <-r.Context().Done():
		return
	case <-ex.done:
		t.mu.Lock()
		if t.entries[id] == ex {
			delete(t.entries, id)
		}
		t.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}
}
