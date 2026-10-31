package neon

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Background sweeps are paced; customer requests never wait behind them.
func TestBackgroundRequestsArePacedAndCustomerRequestsAreNot(t *testing.T) {
	var requests atomic.Int64
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeResponse(t, w, http.StatusOK, map[string]any{})
	}))
	const interval = 40 * time.Millisecond
	p.backgroundInterval = interval
	background := managedpostgres.WithBackgroundRequests(context.Background())

	start := time.Now()
	for range 4 {
		if err := p.doJSON(background, http.MethodGet, "/projects/p", nil, nil, nil, http.StatusOK); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < 3*interval {
		t.Fatalf("four background requests took %s; want at least %s", elapsed, 3*interval)
	}

	// A sweep has a minute of slots reserved: a customer request still goes now.
	p.pacer.mu.Lock()
	p.pacer.next = time.Now().Add(time.Minute)
	p.pacer.mu.Unlock()
	start = time.Now()
	if err := p.doJSON(context.Background(), http.MethodGet, "/projects/p", nil, nil, nil, http.StatusOK); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("customer request waited %s behind background requests", elapsed)
	}

	// A background request that cannot get a slot before its deadline never
	// reaches Neon.
	before := requests.Load()
	ctx, cancel := context.WithTimeout(background, 20*time.Millisecond)
	defer cancel()
	if err := p.doJSON(ctx, http.MethodGet, "/projects/p", nil, nil, nil, http.StatusOK); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("paced request past its deadline = %v", err)
	}
	if requests.Load() != before {
		t.Fatal("a request whose deadline passed while paced was sent")
	}
}
