package main

import (
	"context"
	"sync"
	"time"
)

// idWorkQueue is the bounded fast path for LISTEN/NOTIFY work. PostgreSQL
// notifications are hints — the durable worker remains the recovery path —
// so dropping a notification when the queue is full is safe. The pending set
// also coalesces duplicate notifications for the same build while its work is
// already queued or running.
type idWorkQueue struct {
	ctx    context.Context
	jobs   chan string
	handle func(context.Context, string)
	mu     sync.Mutex
	// A nonzero timestamp means accepted but not yet dispatched to the
	// handler. Zero means active. The same set still coalesces both phases.
	pending  map[string]time.Time
	workers  int
	outcomes [queueOutcomeCount]uint64
	wg       sync.WaitGroup
}

func newIDWorkQueue(ctx context.Context, capacity, workers int, handle func(context.Context, string)) *idWorkQueue {
	if capacity < 1 {
		capacity = 1
	}
	if workers < 1 {
		workers = 1
	}
	q := &idWorkQueue{
		ctx:     ctx,
		jobs:    make(chan string, capacity),
		handle:  handle,
		pending: make(map[string]time.Time),
		workers: workers,
	}
	q.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go q.worker()
	}
	return q
}

// Enqueue returns false for an empty ID, stopping workers, an already pending
// ID or a full bounded queue. Durable recovery handles dropped hints, so callers
// must not block the LISTEN loop here.
func (q *idWorkQueue) Enqueue(id string) bool {
	if q == nil {
		return false
	}
	if id == "" {
		return q.rejectEnqueue(queueInvalid)
	}
	select {
	case <-q.ctx.Done():
		return q.rejectEnqueue(queueStopping)
	default:
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.pending[id]; ok {
		q.outcomes[queueCoalesced]++
		return false
	}
	select {
	case q.jobs <- id:
		q.pending[id] = time.Now().UTC()
		q.outcomes[queueAccepted]++
		return true
	default:
		q.outcomes[queueFull]++
		return false
	}
}

func (q *idWorkQueue) worker() {
	defer q.wg.Done()
	for {
		select {
		case <-q.ctx.Done():
			return
		case id := <-q.jobs:
			q.mu.Lock()
			q.pending[id] = time.Time{}
			q.mu.Unlock()
			q.handle(q.ctx, id)
			q.mu.Lock()
			delete(q.pending, id)
			q.mu.Unlock()
		}
	}
}

func (q *idWorkQueue) Wait() {
	if q != nil {
		q.wg.Wait()
	}
}

// WaitContext waits for queue workers to stop without allowing a broken VM
// driver or handler to hold daemon shutdown forever.
func (q *idWorkQueue) WaitContext(ctx context.Context) error {
	if q == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
