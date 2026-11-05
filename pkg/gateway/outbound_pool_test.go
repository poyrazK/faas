// adr: 20261105133000 — outbound pools remain bounded and recover after saturation.
package gateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestOutboundRedisPoolSaturationAndRecovery(t *testing.T) {
	for _, kind := range []string{"cache", "retry-budget"} {
		t.Run(kind, func(t *testing.T) {
			server := miniredis.RunT(t)
			var client *redis.Client
			var operation func(context.Context) error
			if kind == "cache" {
				cache, err := NewRedisResponseCache(t.Context(), "redis://"+server.Addr())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = cache.Close() })
				client = cache.client.(*redis.Client)
				operation = func(context.Context) error {
					_, err := cache.Get(CacheKey{AppID: "pool-pressure", NormalizedPath: "/"})
					return err
				}
			} else {
				budget, err := NewRedisRetryBudget(t.Context(), "redis://"+server.Addr(), time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = budget.Close() })
				client = budget.remote.(*redis.Client)
				operation = func(ctx context.Context) error {
					if !budget.ObserveOriginal(ctx, "pool-pressure") {
						return errors.New("retry-budget observation failed")
					}
					return nil
				}
			}
			const slots, callers = 8, 32
			held := make([]*redis.Conn, 0, slots)
			for range slots {
				conn := client.Conn()
				t.Cleanup(func() { _ = conn.Close() })
				if err := conn.Ping(t.Context()).Err(); err != nil {
					t.Fatal(err)
				}
				held = append(held, conn)
			}
			type result struct {
				err     error
				elapsed time.Duration
			}
			start, results := make(chan struct{}), make(chan result, callers)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			var workers sync.WaitGroup
			for range callers {
				workers.Go(func() {
					<-start
					began := time.Now()
					err := operation(ctx)
					results <- result{err, time.Since(began)}
				})
			}
			close(start)
			workers.Wait()
			close(results)
			for r := range results {
				if r.err == nil {
					t.Error("operation exceeded the occupied pool's connection cap")
				}
				// Allow scheduling overhead around the 100ms operation budget,
				// while rejecting a regression to the SDK's multi-second waits.
				if r.elapsed > 500*time.Millisecond {
					t.Errorf("pool wait took %s, want at most 500ms including scheduling overhead", r.elapsed)
				}
			}
			if ctx.Err() != nil {
				t.Fatal("pool waits ignored their operation/pool timeout")
			}
			if stats := client.PoolStats(); stats.TotalConns != slots || stats.IdleConns != 0 {
				t.Fatalf("pool overflowed while pinned: %+v", stats)
			}
			for _, conn := range held {
				_ = conn.Close()
			}
			if err := operation(ctx); err != nil {
				t.Fatalf("pool did not recover after pressure subsided: %v", err)
			}
		})
	}
}
