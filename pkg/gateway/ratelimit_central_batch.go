package gateway

import (
	"context"
	"errors"
	"sync"
	"time"
)

// CentralBatchBackend is an optional CentralBackend extension that consumes up
// to n tokens from one central counter in a single statement. A Limiter whose
// backend implements it coalesces concurrent consults for the same counter.
//
// Without coalescing every request held one gatewayd-internal pool connection
// for its consult. Requests for one app all update the same counter row, so
// under load they queued on that row and on the pool. On production-us, a 200
// client surge made ~146 consult attempts per second per gateway while only
// ~20 got a connection, and the rest timed out waiting.
type CentralBatchBackend interface {
	// ConsumeTokens grants min(n, available) tokens. granted is how many
	// of the n requests are admitted; remaining is the balance after the
	// grant.
	ConsumeTokens(ctx context.Context, scope, subjectID, plan string, rps, burst float64, n int) (granted, remaining int, err error)
}

// errCentralBatchWait marks a request whose batched consume did not finish
// within centralConsultTimeout of joining it.
var errCentralBatchWait = errors.New("ratelimit central: batched consume did not finish in time")

type centralBatchKey struct {
	scope, subjectID, plan string
}

// centralBatch collects the requests that arrive while a consult for the
// same counter is in flight. They are charged together by one statement.
type centralBatch struct {
	n          int
	rps, burst float64

	done      chan struct{}
	granted   int
	remaining int
	err       error
}

// centralCoalescer keeps at most one central statement in flight per counter
// per Limiter. The first request consults alone, exactly as before; requests
// arriving during that consult join the next batch, which runs when it ends.
// Every request is still charged against the shared counter, so the
// cross-replica limit is unchanged; only the statement count per counter
// drops from one per request to at most one per round trip.
type centralCoalescer struct {
	mu sync.Mutex
	// inflight holds a key while a statement for it is running. Its value is
	// the batch collecting joiners for the next statement, or nil.
	inflight map[centralBatchKey]*centralBatch
}

func (c *centralCoalescer) consume(ctx context.Context, single CentralBackend, batch CentralBatchBackend, scope, subjectID, plan string, rps, burst float64) (int, bool, error) {
	key := centralBatchKey{scope: scope, subjectID: subjectID, plan: plan}
	c.mu.Lock()
	if c.inflight == nil {
		c.inflight = make(map[centralBatchKey]*centralBatch)
	}
	next, busy := c.inflight[key]
	if !busy {
		c.inflight[key] = nil
		c.mu.Unlock()
		consultCtx, cancel := context.WithTimeout(ctx, centralConsultTimeout)
		remaining, admitted, err := single.ConsumeToken(consultCtx, scope, subjectID, plan, rps, burst)
		cancel()
		c.handOff(ctx, key, batch)
		return remaining, admitted, err
	}
	if next == nil {
		next = &centralBatch{done: make(chan struct{})}
		c.inflight[key] = next
	}
	position := next.n
	next.n++
	next.rps, next.burst = rps, burst
	c.mu.Unlock()

	timer := time.NewTimer(centralConsultTimeout)
	defer timer.Stop()
	select {
	case <-next.done:
	case <-ctx.Done():
		return 0, false, ctx.Err()
	case <-timer.C:
		return 0, false, errCentralBatchWait
	}
	if next.err != nil {
		return 0, false, next.err
	}
	if position < next.granted {
		// Report the balance a sequential consume at this position would
		// have seen, so response headers stay meaningful.
		return next.remaining + next.granted - position - 1, true, nil
	}
	return next.remaining, false, nil
}

// handOff releases key after a statement, or starts the batch that queued
// behind it. The batch keeps the leader's context values but not its
// cancellation: it answers for every request in the batch, not the leader.
func (c *centralCoalescer) handOff(ctx context.Context, key centralBatchKey, batch CentralBatchBackend) {
	c.mu.Lock()
	next := c.inflight[key]
	if next == nil {
		delete(c.inflight, key)
		c.mu.Unlock()
		return
	}
	c.inflight[key] = nil
	c.mu.Unlock()
	go c.runBatches(context.WithoutCancel(ctx), key, next, batch)
}

func (c *centralCoalescer) runBatches(ctx context.Context, key centralBatchKey, b *centralBatch, batch CentralBatchBackend) {
	for b != nil {
		batchCtx, cancel := context.WithTimeout(ctx, centralConsultTimeout)
		b.granted, b.remaining, b.err = batch.ConsumeTokens(batchCtx, key.scope, key.subjectID, key.plan, b.rps, b.burst, b.n)
		cancel()
		close(b.done)

		c.mu.Lock()
		b = c.inflight[key]
		if b == nil {
			delete(c.inflight, key)
		} else {
			c.inflight[key] = nil
		}
		c.mu.Unlock()
	}
}
