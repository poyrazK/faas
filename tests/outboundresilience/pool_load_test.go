//go:build load

package outboundresilience_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// These tests require explicit fixture endpoints. They never infer a deployed
// database or Redis endpoint from the daemon's ambient configuration.
func fixtureURL(t *testing.T, variable string) string {
	t.Helper()
	value := os.Getenv(variable)
	if value == "" {
		t.Fatalf("%s must name an isolated test service", variable)
	}
	return value
}

func profileInt(t *testing.T, variable string, fallback, maximum int) int {
	t.Helper()
	value := os.Getenv(variable)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > maximum {
		t.Fatalf("%s must be an integer in [1, %d]", variable, maximum)
	}
	return n
}

func profileDuration(t *testing.T, variable string, fallback time.Duration) time.Duration {
	t.Helper()
	value := os.Getenv(variable)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 || d > 10*time.Second {
		t.Fatalf("%s must be a positive duration at most 10s", variable)
	}
	return d
}

func runPoolLoad(t *testing.T, operation func(context.Context) error) {
	t.Helper()
	workers := profileInt(t, "FAAS_RESILIENCE_CONCURRENCY", 32, 128)
	iterations := profileInt(t, "FAAS_RESILIENCE_ITERATIONS", 20, 1000)
	requestBudget := profileDuration(t, "FAAS_RESILIENCE_REQUEST_BUDGET", time.Second)
	p95Budget := profileDuration(t, "FAAS_RESILIENCE_P95_BUDGET", 250*time.Millisecond)
	if p95Budget > requestBudget {
		t.Fatal("p95 budget must not exceed the request budget")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	type result struct {
		elapsed time.Duration
		err     error
	}
	results := make(chan result, workers*iterations)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			<-start
			for range iterations {
				requestCtx, cancelRequest := context.WithTimeout(ctx, requestBudget)
				began := time.Now()
				err := operation(requestCtx)
				if err == nil {
					err = requestCtx.Err()
				}
				results <- result{time.Since(began), err}
				cancelRequest()
				if ctx.Err() != nil {
					return
				}
			}
		})
	}
	began := time.Now()
	close(start)
	wg.Wait()
	elapsed := time.Since(began)
	close(results)
	latencies := make([]time.Duration, 0, workers*iterations)
	failed := 0
	var firstError error
	for r := range results {
		latencies = append(latencies, r.elapsed)
		if r.err != nil {
			failed++
			if firstError == nil {
				firstError = r.err
			}
		}
	}
	slices.Sort(latencies)
	p95 := latencies[(len(latencies)*95+99)/100-1]
	t.Logf("concurrency=%d operations=%d failures=%d throughput=%.1f_ops/s p50=%s p95=%s max=%s",
		workers, len(latencies), failed, float64(len(latencies))/elapsed.Seconds(),
		latencies[(len(latencies)-1)/2], p95, latencies[len(latencies)-1])
	if failed > 0 {
		t.Fatalf("%d operations failed under concurrency: %v", failed, firstError)
	}
	if len(latencies) != workers*iterations {
		t.Errorf("load stopped after %d of %d operations: %v", len(latencies), workers*iterations, ctx.Err())
	}
	if p95 > p95Budget {
		t.Errorf("p95 %s exceeded configured headroom budget %s", p95, p95Budget)
	}
}

func TestOutboundPostgresPoolLoad(t *testing.T) {
	size := profileInt(t, "FAAS_RESILIENCE_PG_POOL_SIZE", 4, 32)
	pool, err := db.NewBoundedPool(t.Context(), fixtureURL(t, "FAAS_RESILIENCE_DATABASE_URL"), int32(size))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// Fill every slot before measuring so lazy connection setup does not
	// disguise a too-small pool or dominate the steady-state result.
	func() {
		for range size {
			conn, err := pool.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
		}
	}()
	runPoolLoad(t, func(ctx context.Context) error {
		_, err := pool.Exec(ctx, "SELECT pg_sleep(0.005)")
		return err
	})
	stats := pool.Stat()
	t.Logf("pool_limit=%d connections=%d acquired=%d waited_acquires=%d total_acquire_time=%s",
		size, stats.TotalConns(), stats.AcquiredConns(), stats.EmptyAcquireCount(), stats.AcquireDuration())
	if stats.TotalConns() > int32(size) || stats.AcquiredConns() != 0 {
		t.Fatal("load overflowed the pool or leaked acquired connections")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pool did not recover after load: %v", err)
	}
}

type budgetErrors struct{ count atomic.Int64 }

func (b *budgetErrors) RecordRetryBudgetOperation(_, result string) {
	if result == "error" {
		b.count.Add(1)
	}
}

func TestOutboundRedisPoolsLoad(t *testing.T) {
	endpoint := fixtureURL(t, "FAAS_RESILIENCE_REDIS_URL")
	t.Run("response-cache", func(t *testing.T) {
		cache, err := gateway.NewRedisResponseCache(t.Context(), endpoint)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cache.Close() })
		runPoolLoad(t, func(context.Context) error {
			_, err := cache.Get(gateway.CacheKey{AppID: "outbound-pool-load", NormalizedPath: "/missing"})
			return err
		})
	})
	t.Run("retry-budget", func(t *testing.T) {
		budget, err := gateway.NewRedisRetryBudget(t.Context(), endpoint, 5*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = budget.Close() })
		observer := &budgetErrors{}
		budget.WithObserver(observer)
		t.Cleanup(func() {
			if n := observer.count.Load(); n != 0 {
				t.Errorf("%d Redis budget operations failed", n)
			}
		})
		scope := fmt.Sprintf("outbound-pool-load-%d", time.Now().UnixNano())
		runPoolLoad(t, func(ctx context.Context) error {
			if !budget.ObserveOriginal(ctx, scope) {
				return fmt.Errorf("retry-budget original observation failed")
			}
			if !budget.AllowRetry(ctx, scope, 100, 0) {
				return fmt.Errorf("retry allowance lost under concurrency")
			}
			return nil
		})
		if budget.AllowRetry(t.Context(), scope, 100, 0) {
			t.Error("concurrent operations created extra retry allowance")
		}
	})
}
