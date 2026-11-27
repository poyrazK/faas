// adr: 463 — a stale runtime-base staging failure resumes from its checkpoint.
package imaged

import (
	"errors"
	"sync"
	"time"
)

// errRuntimeBaseStale marks a runtime-base staging failure caused by another
// node publishing a different base for the same key (the publication saw
// state.ErrApplicationStandardRuntimeStale). During a rollout, release N and
// N-1 pin different runner bases and each node's staging moves the key's
// current producer, so the other release's staging is refused
// (production, 2026-11-27: "runtime inputs changed after admission" on the
// node not yet updated). Staging is idempotent, so the preparation is left
// for the recovery loop instead of failing the deployment: once the node
// runs the same release as the fleet, or the race is over, it succeeds.
var errRuntimeBaseStale = errors.New("imaged: another node changed the runtime base while it was staged")

const (
	// runtimeBaseRetryEvery holds a deferred preparation back between
	// attempts; the recovery loop itself ticks every 2 s, and each attempt
	// re-stages the base.
	runtimeBaseRetryEvery = 30 * time.Second
	// runtimeBaseRetryWindow bounds how long a deployment waits for its node
	// to converge with the fleet before it fails as it did before.
	runtimeBaseRetryWindow = 30 * time.Minute
)

// runtimeBaseRetries tracks deployments whose stale runtime-base staging is
// deferred to the recovery loop. It lives in memory: a restart, usually the
// node's own update, retries every deployment at once. A nil value defers
// nothing.
type runtimeBaseRetries struct {
	mu      sync.Mutex
	now     func() time.Time
	pending map[string]runtimeBaseRetry
}

type runtimeBaseRetry struct{ first, next time.Time }

func newRuntimeBaseRetries() *runtimeBaseRetries {
	return &runtimeBaseRetries{now: time.Now, pending: map[string]runtimeBaseRetry{}}
}

// hold reports whether a stale-base failure of deployment id is left for the
// recovery loop, and holds its next attempt back by runtimeBaseRetryEvery.
// Past runtimeBaseRetryWindow from its first stale failure the deployment is
// forgotten and hold reports false, so the failure stands.
func (r *runtimeBaseRetries) hold(id string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	retry, ok := r.pending[id]
	if !ok {
		retry.first = now
	}
	if now.Sub(retry.first) >= runtimeBaseRetryWindow {
		delete(r.pending, id)
		return false
	}
	retry.next = now.Add(runtimeBaseRetryEvery)
	r.pending[id] = retry
	return true
}

// due reports whether deployment id may be attempted now.
func (r *runtimeBaseRetries) due(id string) bool {
	if r == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	retry, ok := r.pending[id]
	return !ok || !r.now().Before(retry.next)
}

// forget drops deployment id once an attempt ends other than stale.
func (r *runtimeBaseRetries) forget(id string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pending, id)
}
