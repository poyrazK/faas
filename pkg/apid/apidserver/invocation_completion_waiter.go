package apidserver

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
	// invocationCompletionRecheck is how often a waiter re-reads its row.
	// pg_notify is not durable: a notification sent while apid's LISTEN
	// connection reconnects, or dropped from a full buffer, is gone, and the
	// request then waited out its whole timeout (bug hunt 2026-10-28 B24).
	invocationCompletionRecheck = 2 * time.Second
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
	waiters map[string]map[*completionWait]struct{}
	recent  map[string]time.Time
	// order holds recent completions oldest first, so pruning is amortized
	// O(1) instead of a scan of every recent entry on each notification.
	order []recentCompletion
	// appWaiters are queue receives waiting for the next completion of an
	// app; each gets the invocation ID once.
	appWaiters map[string]map[chan string]struct{}
}

// completionWait is one blocked Wait. ch is closed when the invocation
// completed (completed is set first) or when notifications may have been
// missed, after which the waiter re-reads its row.
type completionWait struct {
	ch        chan struct{}
	completed bool
}

type recentCompletion struct {
	id string
	at time.Time
}

func newInvocationCompletionWaiter(pool *pgxpool.Pool, log *slog.Logger) *invocationCompletionWaiter {
	if log == nil {
		log = slog.Default()
	}
	return &invocationCompletionWaiter{
		pool:       pool,
		log:        log,
		waiters:    make(map[string]map[*completionWait]struct{}),
		recent:     make(map[string]time.Time),
		appWaiters: make(map[string]map[chan string]struct{}),
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
	notif, err := db.SubscribeWithResync(ctx, w.pool, []string{db.NotifyInvocationDone}, w.log)
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
			if n.Channel == db.ResyncChannel {
				w.wakeAll()
				continue
			}
			w.completeFromPayload(n.Payload)
		}
	}
}

func (w *invocationCompletionWaiter) completeFromPayload(payload string) {
	var event struct {
		InvocationID string `json:"invocation_id"`
		AppID        string `json:"app_id"`
	}
	if err := json.Unmarshal([]byte(payload), &event); err != nil || event.InvocationID == "" {
		return
	}
	w.complete(event.InvocationID, event.AppID)
}

func (w *invocationCompletionWaiter) complete(id, appID string) {
	now := time.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.recent[id] = now
	w.order = append(w.order, recentCompletion{id: id, at: now})
	for cw := range w.waiters[id] {
		cw.completed = true
		close(cw.ch)
	}
	delete(w.waiters, id)
	if appID != "" {
		for ch := range w.appWaiters[appID] {
			ch <- id // buffered; each receive is delivered to once
		}
		delete(w.appWaiters, appID)
	}
	w.pruneRecentLocked(now)
}

// WaitApp blocks until an invocation of appID completes and returns its ID,
// the caller's cancellation fires, or timeout passes (db.ErrWaitTimeout).
// Queue receives used to LISTEN on a pooled database connection each, so a
// dozen long-polls exhausted apid's pool (bug hunt 2026-10-28 B21). The ID
// is an untrusted hint: the caller re-reads the row.
func (w *invocationCompletionWaiter) WaitApp(ctx context.Context, appID string, timeout time.Duration) (string, error) {
	if w == nil || appID == "" {
		return "", errors.New("invocation completion waiter: invalid request")
	}
	if timeout <= 0 {
		return "", db.ErrWaitTimeout
	}
	ch := make(chan string, 1)
	w.mu.Lock()
	if w.appWaiters == nil {
		w.appWaiters = make(map[string]map[chan string]struct{})
	}
	if w.appWaiters[appID] == nil {
		w.appWaiters[appID] = make(map[chan string]struct{})
	}
	w.appWaiters[appID][ch] = struct{}{}
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if set := w.appWaiters[appID]; set != nil {
			delete(set, ch)
			if len(set) == 0 {
				delete(w.appWaiters, appID)
			}
		}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case id := <-ch:
		return id, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", db.ErrWaitTimeout
	}
}

// wakeAll makes every waiter re-read its row: notifications may have been
// missed while the LISTEN connection was down or a buffer was full.
func (w *invocationCompletionWaiter) wakeAll() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, set := range w.waiters {
		for cw := range set {
			close(cw.ch)
		}
		delete(w.waiters, id)
	}
}

func (w *invocationCompletionWaiter) pruneRecentLocked(now time.Time) {
	cutoff := now.Add(-invocationCompletionRecentTTL)
	drop := 0
	for drop < len(w.order) {
		head := w.order[drop]
		if !head.at.Before(cutoff) && len(w.recent) <= invocationCompletionRecentCap {
			break
		}
		// A later completion of the same id owns the map entry.
		if at, ok := w.recent[head.id]; ok && at.Equal(head.at) {
			delete(w.recent, head.id)
		}
		drop++
	}
	w.order = w.order[drop:]
}

// Wait blocks until the invocation completes, the caller's timeout or
// cancellation fires, or settled (when non-nil) reports the row finished on a
// periodic or resync recheck. Registration and the recent-event check share
// one lock, closing the enqueue→LISTEN race that existed in the old
// per-request implementation.
func (w *invocationCompletionWaiter) Wait(ctx context.Context, id string, timeout time.Duration, settled func(context.Context) bool) error {
	if w == nil || id == "" {
		return errors.New("invocation completion waiter: invalid request")
	}
	if timeout <= 0 {
		return db.ErrWaitTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	recheck := time.NewTicker(invocationCompletionRecheck)
	defer recheck.Stop()
	for {
		cw, done := w.register(id)
		if done {
			return nil
		}
		select {
		case <-cw.ch:
			if cw.completed || (settled != nil && settled(ctx)) {
				return nil
			}
		case <-recheck.C:
			w.remove(id, cw)
			if settled != nil && settled(ctx) {
				return nil
			}
		case <-ctx.Done():
			w.remove(id, cw)
			return ctx.Err()
		case <-timer.C:
			w.remove(id, cw)
			return db.ErrWaitTimeout
		}
	}
}

// register adds a waiter for id, or reports that id completed recently.
func (w *invocationCompletionWaiter) register(id string) (*completionWait, bool) {
	now := time.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pruneRecentLocked(now)
	if at, ok := w.recent[id]; ok && !at.Before(now.Add(-invocationCompletionRecentTTL)) {
		return nil, true
	}
	if w.waiters[id] == nil {
		w.waiters[id] = make(map[*completionWait]struct{})
	}
	cw := &completionWait{ch: make(chan struct{})}
	w.waiters[id][cw] = struct{}{}
	return cw, false
}

func (w *invocationCompletionWaiter) remove(id string, cw *completionWait) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if waiters := w.waiters[id]; waiters != nil {
		delete(waiters, cw)
		if len(waiters) == 0 {
			delete(w.waiters, id)
		}
	}
}
