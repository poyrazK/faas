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
)

type tracker struct {
	mu      sync.Mutex
	entries map[string]chan struct{}
}

// Wrap marks completion after the inner handler has closed its guest body
// and connection. Cancelling the inbound H2 stream is not itself completion.
// Entries remain until vmmd acknowledges them; its permits bound the set.
func Wrap(next http.Handler) http.Handler {
	t := &tracker{entries: make(map[string]chan struct{})}
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
		done, ok := t.begin(id)
		if !ok {
			http.Error(w, "invalid bridge exchange", http.StatusServiceUnavailable)
			return
		}
		defer close(done)
		next.ServeHTTP(w, r)
	})
}

func (t *tracker) begin(id string) (chan struct{}, bool) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.entries[id]; exists || len(t.entries) >= api.TrafficBridgeCompletionMaxEntries {
		return nil, false
	}
	done := make(chan struct{})
	t.entries[id] = done
	return done, true
}

func (t *tracker) wait(w http.ResponseWriter, r *http.Request, id string) {
	t.mu.Lock()
	done := t.entries[id]
	t.mu.Unlock()
	if done == nil {
		http.Error(w, "unknown bridge exchange", http.StatusNotFound)
		return
	}
	select {
	case <-r.Context().Done():
		return
	case <-done:
		t.mu.Lock()
		delete(t.entries, id)
		t.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}
}
