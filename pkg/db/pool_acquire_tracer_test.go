package db

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// silentServer accepts TCP connections and never answers the Postgres
// startup message, so a pool dialing it waits until the caller gives up.
func silentServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return ln.Addr().String()
}

// unusedAddr returns an address nothing listens on, so a dial is refused.
func unusedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func instrumentedPool(t *testing.T, addr string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://faas@" + addr + "/faas?sslmode=disable&connect_timeout=2")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	cfg.MaxConns = 1
	cfg.MinConns = 0
	instrumentAcquires(cfg)
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestCanceledAcquiresCountOnlyAbandonedWaits pins H10-108: FaasDBPoolStarved
// paged on schedd with idle connections because pgxpool counts an
// acquisition whose context had already ended. The exported counter must
// count only callers that gave up while waiting.
func TestCanceledAcquiresCountOnlyAbandonedWaits(t *testing.T) {
	cases := map[string]struct {
		addr          func(*testing.T) string
		ctx           func() (context.Context, context.CancelFunc)
		wantAbandoned float64
	}{
		"context ended before the acquire": {
			addr: silentServer,
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
		},
		"caller gave up while waiting": {
			addr: silentServer,
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 100*time.Millisecond)
			},
			wantAbandoned: 1,
		},
		"dial refused under a live context": {
			addr: unusedAddr,
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 10*time.Second)
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			pool := instrumentedPool(t, tc.addr(t))
			ctx, cancel := tc.ctx()
			defer cancel()
			if _, err := pool.Acquire(ctx); err == nil {
				t.Fatal("Acquire succeeded, want an error")
			}
			got := value(t, gather(t, NewPoolCollector("testd", pool)), "testd_db_pool_canceled_acquires_total")
			if got != tc.wantAbandoned {
				t.Errorf("canceled_acquires_total = %v, want %v (pgxpool's own count: %d)",
					got, tc.wantAbandoned, pool.Stat().CanceledAcquireCount())
			}
		})
	}
}

// TestPoolCollectorFallsBackWithoutTracer keeps pools built outside open()
// reporting pgxpool's own count rather than a silent zero.
func TestPoolCollectorFallsBackWithoutTracer(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://faas@" + silentServer(t) + "/faas?sslmode=disable&connect_timeout=2")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	t.Cleanup(pool.Close)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pool.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire err = %v, want context.Canceled", err)
	}
	if got := value(t, gather(t, NewPoolCollector("testd", pool)), "testd_db_pool_canceled_acquires_total"); got != 1 {
		t.Errorf("canceled_acquires_total without the tracer = %v, want pgxpool's count 1", got)
	}
}
