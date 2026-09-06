package main

import (
	"context"
	"sync"
)

// idWorkQueue is the bounded fast path for LISTEN/NOTIFY work. PostgreSQL
// notifications are hints — the durable worker remains the recovery path —
// so dropping a notification when the queue is full is safe. The pending set
// also coalesces duplicate notifications for the same build while its work is
// already queued or running.
type idWorkQueue struct {
	ctx     context.Context
	jobs    chan string
	handle  func(context.Context, string)
	mu      sync.Mutex
	pending map[string]struct{}
	wg      sync.WaitGroup
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
		pending: make(map[string]struct{}),
	}
	q.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go q.worker()
	}
	return q
}

// Enqueue returns false when the ID is already pending or the bounded queue
// is full. The durable polling worker will discover a build whose notification
// was dropped, so callers must not block the LISTEN loop here.
func (q *idWorkQueue) Enqueue(id string) bool {
	if q == nil || id == "" {
		return false
	}
	select {
	case <-q.ctx.Done():
		return false
	default:
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.pending[id]; ok {
		return false
	}
	select {
	case q.jobs <- id:
		q.pending[id] = struct{}{}
		return true
	default:
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
