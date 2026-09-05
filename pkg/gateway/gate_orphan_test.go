package gateway

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWakeGateForgetsCompletionAfterAllWaitersLeave(t *testing.T) {
	for _, mode := range []string{"success", "failure", "deadline", "abort"} {
		t.Run(mode, func(t *testing.T) {
			ttl := 3 * time.Second
			if mode == "deadline" {
				ttl = 100 * time.Millisecond
			}
			g := NewWakeGate(8, ttl)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{})
			finish := make(chan struct{})
			returned := make(chan error, 1)
			var shouldAbort func() bool
			if mode == "abort" {
				shouldAbort = func() bool { return g.InflightWaiters("app") == 0 }
			}
			go func() {
				returned <- g.Wait(ctx, "app", "account", func() bool { return true }, func(ctx context.Context) error {
					close(started)
					select {
					case <-finish:
						if mode == "failure" {
							return errors.New("old wake failed")
						}
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}, shouldAbort, nil)
			}()
			<-started
			g.mu.Lock()
			call := g.inflight["app"]
			g.mu.Unlock()
			cancel()
			if err := <-returned; !errors.Is(err, context.Canceled) {
				t.Fatalf("departing waiter: %v", err)
			}
			if mode == "success" || mode == "failure" {
				close(finish)
			}
			select {
			case <-call.done:
			case <-time.After(2 * time.Second):
				t.Fatal("orphaned wake did not complete")
			}
			g.mu.Lock()
			_, retained := g.inflight["app"]
			g.mu.Unlock()
			if retained {
				t.Error("completed wake retained after every waiter departed")
			}
			fresh := false
			if err := g.Wait(context.Background(), "app", "account", func() bool { return true }, func(context.Context) error {
				fresh = true
				return nil
			}, nil, nil); err != nil {
				t.Fatalf("fresh request inherited old wake error: %v", err)
			}
			if !fresh {
				t.Fatal("fresh request reused the completed orphan instead of waking")
			}
		})
	}
}
