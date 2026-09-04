package gateway

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
)

func TestNodeClientCacheCoalescesConcurrentFirstDial(t *testing.T) {
	previousResolver := resolveNodeTarget
	t.Cleanup(func() { resolveNodeTarget = previousResolver })

	var resolveCalls atomic.Int32
	SetNodeResolver(func(context.Context, string) (string, bool) {
		resolveCalls.Add(1)
		return "node-target", true
	})

	dialStarted := make(chan struct{})
	releaseDial := make(chan struct{})
	var dialCalls atomic.Int32
	var signalOnce sync.Once
	cache := NewNodeClientCache(func(context.Context, string) (*grpc.ClientConn, error) {
		dialCalls.Add(1)
		signalOnce.Do(func() { close(dialStarted) })
		<-releaseDial
		return &grpc.ClientConn{}, nil
	}, nil)

	const callers = 32
	results := make(chan bool, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			_, closer, ok := cache.ClientFor(context.Background(), "node-1")
			if ok {
				_ = closer.Close()
			}
			results <- ok
		}()
	}

	select {
	case <-dialStarted:
	case <-time.After(time.Second):
		t.Fatal("first node dial did not start")
	}
	close(releaseDial)
	wg.Wait()
	close(results)

	for ok := range results {
		if !ok {
			t.Fatal("concurrent ClientFor returned ok=false")
		}
	}
	if got := resolveCalls.Load(); got != 1 {
		t.Fatalf("resolve calls = %d, want 1", got)
	}
	if got := dialCalls.Load(); got != 1 {
		t.Fatalf("dial calls = %d, want 1", got)
	}
}
