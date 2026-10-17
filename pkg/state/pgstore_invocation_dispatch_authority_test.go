//go:build !no_pg

package state_test

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"sync"
	"testing"
	"time"
)

type dispatchQueryCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

func (c *dispatchQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	// AppByID still uses the legacy app projection rather than its sqlc query.
	if strings.HasSuffix(data.SQL, " from apps where id = $1") {
		c.counts["AppByID"]++
	}
	for _, name := range []string{"AppByID", "ReadInvocationDispatchAuthority", "ReadInvocationEnvironmentOwner", "ReadQueueCanary", "QueueCanarySnapshot"} {
		if strings.HasPrefix(data.SQL, "-- name: "+name+" ") {
			c.counts[name]++
		}
	}
	return ctx
}
func (*dispatchQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestPgDispatchAuthorityOrdinaryQueryBudget(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, app, _ := seedLiveDeploy(t, store, ctx, "dispatch-budget", "dispatch-budget")
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account, AppID: app, Source: state.InvocationQueue, QueueName: "orders", State: state.InvocationPending, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	counter := &dispatchQueryCounter{counts: map[string]int{}}
	config := pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	traced, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	if _, _, err = state.ResolveInvocationVersion(ctx, state.NewPgStore(traced), inv); err != nil {
		t.Fatal(err)
	}
	counter.mu.Lock()
	defer counter.mu.Unlock()
	if counter.counts["AppByID"] != 1 || counter.counts["ReadInvocationDispatchAuthority"] != 1 || counter.counts["ReadInvocationEnvironmentOwner"] != 1 || counter.counts["ReadQueueCanary"] != 0 || counter.counts["QueueCanarySnapshot"] != 0 {
		t.Fatalf("dispatch SQL reads: %+v", counter.counts)
	}
}
