package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
)

const (
	invocationCompletionRecentTTL = time.Minute
	invocationCompletionRecentCap = 16384
)

// invocationCompletionWaiter multiplexes one PostgreSQL LISTEN session to
// all synchronous invocation requests in this apid process. The old path
// acquired a pool connection and issued LISTEN for every request, which made
// the small default pool the throughput ceiling during a burst.
//
// The final invocation row is still read by the handler after Wait returns;
// the notification is only a wake-up signal and never carries the result
// body. This preserves large-result semantics and keeps the notification
// payload bounded.
type invocationCompletionWaiter struct {
	pool *pgxpool.Pool
	log  *slog.Logger

	mu      sync.Mutex
	waiters map[string]map[chan struct{}]struct{}
	recent  map[string]time.Time
}

func newInvocationCompletionWaiter(pool *pgxpool.Pool, log *slog.Logger) *invocationCompletionWaiter {
	if log == nil {
		log = slog.Default()
	}
	return &invocationCompletionWaiter{
		pool:    pool,
		log:     log,
		waiters: make(map[string]map[chan struct{}]struct{}),
		recent:  make(map[string]time.Time),
	}
}

// Start opens the single reconnecting listener before apid exposes its HTTP
// listener. A failure is returned to the caller so production can retain the
// legacy per-request fallback rather than making boot depend on this
// optimization.
func (w *invocationCompletionWaiter) Start(ctx context.Context) error {
	if w == nil || w.pool == nil {
		return errors.New("invocation completion waiter: nil pool")
	}
	notif, err := db.SubscribeWithReconnect(ctx, w.pool, []string{db.NotifyInvocationDone}, w.log)
	if err != nil {
		return err
	}
	go w.run(ctx, notif)
	return nil
}

func (w *invocationCompletionWaiter) run(ctx context.Context, notif <-chan db.Notification) {
	for {
		select {
		case <-ctx.Done():
			return
		case n, ok := <-notif:
			if !ok {
				return
			}
			w.completeFromPayload(n.Payload)
		}
	}
}

func (w *invocationCompletionWaiter) completeFromPayload(payload string) {
	var event struct {
		InvocationID string `json:"invocation_id"`
	}
	if err := json.Unmarshal([]byte(payload), &event); err != nil || event.InvocationID == "" {
		return
	}
	w.complete(event.InvocationID)
}

func (w *invocationCompletionWaiter) complete(id string) {
	now := time.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.recent[id] = now
	for ch := range w.waiters[id] {
		close(ch)
	}
	delete(w.waiters, id)
	w.pruneRecentLocked(now)
}

func (w *invocationCompletionWaiter) pruneRecentLocked(now time.Time) {
	cutoff := now.Add(-invocationCompletionRecentTTL)
	for id, at := range w.recent {
		if at.Before(cutoff) {
			delete(w.recent, id)
		}
	}
	for len(w.recent) > invocationCompletionRecentCap {
		for id := range w.recent {
			delete(w.recent, id)
			break
		}
	}
}

// Wait blocks until the invocation completion notification arrives or the
// caller's timeout/cancellation fires. Registration and the recent-event
// check share one lock, closing the enqueue→LISTEN race that existed in the
// old per-request implementation.
func (w *invocationCompletionWaiter) Wait(ctx context.Context, id string, timeout time.Duration) error {
	if w == nil || id == "" {
		return errors.New("invocation completion waiter: invalid request")
	}
	if timeout <= 0 {
		return db.ErrWaitTimeout
	}

	ch := make(chan struct{})
	now := time.Now()
	w.mu.Lock()
	w.pruneRecentLocked(now)
	if at, ok := w.recent[id]; ok && !at.Before(now.Add(-invocationCompletionRecentTTL)) {
		w.mu.Unlock()
		return nil
	}
	if w.waiters[id] == nil {
		w.waiters[id] = make(map[chan struct{}]struct{})
	}
	w.waiters[id][ch] = struct{}{}
	w.mu.Unlock()

	defer w.remove(id, ch)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return db.ErrWaitTimeout
	}
}

func (w *invocationCompletionWaiter) remove(id string, ch chan struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if waiters := w.waiters[id]; waiters != nil {
		delete(waiters, ch)
		if len(waiters) == 0 {
			delete(w.waiters, id)
		}
	}
}
