package sched

import (
	"sync"
	"time"
)

// keyedLocks is a set of mutexes by key that keeps a key's mutex only while
// someone holds or waits for it, like the app locks. The per-deployment and
// per-app service locks were plain maps that kept one mutex for every
// deployment and app schedd ever reconciled (ADR 20261104143005).
// The zero value is ready to use.
type keyedLocks struct {
	mu    sync.Mutex
	locks map[string]*appLock
}

// lock takes key's mutex and returns its unlock.
func (k *keyedLocks) lock(key string) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = make(map[string]*appLock)
	}
	l := k.locks[key]
	if l == nil {
		l = &appLock{}
		k.locks[key] = l
	}
	l.refs++
	k.mu.Unlock()

	l.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Unlock()
			k.mu.Lock()
			l.refs--
			if l.refs <= 0 && k.locks[key] == l {
				delete(k.locks, key)
			}
			k.mu.Unlock()
		})
	}
}

// size reports how many keys have a mutex, for tests.
func (k *keyedLocks) size() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.locks)
}

// restartDedupWindow is how long a completed restart's wake ID is remembered
// to drop a duplicate delivery of its notification. Duplicates come from
// pg_notify retries and the notification outbox's replay, well inside it.
const restartDedupWindow = time.Hour

// completedRestart is the last restart finished for an app.
type completedRestart struct {
	wakeID string
	at     time.Time
}

// pruneCompletedRestartsLocked drops restarts older than the dedup window, at
// most once a minute. The caller holds e.restartMu.
func (e *Engine) pruneCompletedRestartsLocked(now time.Time) {
	if now.Sub(e.restartPrunedAt) < time.Minute {
		return
	}
	e.restartPrunedAt = now
	for appID, done := range e.restartCompleted {
		if now.Sub(done.at) > restartDedupWindow {
			delete(e.restartCompleted, appID)
		}
	}
}
