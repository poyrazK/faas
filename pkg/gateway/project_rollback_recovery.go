package gateway

import (
	"context"
	"sync"
	"time"
)

// A watch is installed only after a gateway has refreshed and acknowledged a
// specific project rollback route. It allows one asynchronous write at a time.
type projectRollbackWatch struct {
	generation int64
	deployment string
	appliedAt  time.Time
	expiresAt  time.Time
	record     func(context.Context, time.Time, time.Time) (bool, error)

	mu      sync.Mutex
	writing bool
	done    bool
	nextTry time.Time
}

func (b *PGBackend) ProjectRollbackTargetInstalled(app, deployment string) bool {
	b.tgtMu.RLock()
	defer b.tgtMu.RUnlock()
	picker := b.appsPicker[app]
	return picker != nil && picker.weightsAuthoritative && len(picker.weights) == 1 &&
		picker.weights[0].DeploymentID == deployment && picker.weights[0].Percent == 100
}

// HasProjectRollbackRoute lets retries reach restarted gateways without
// refreshing the response cache on gateways that applied it already.
func (b *PGBackend) HasProjectRollbackRoute(app string, generation int64) bool {
	watch, ok := b.projectRollbackWatches.Load(app)
	return ok && watch.(*projectRollbackWatch).generation >= generation
}

func (b *PGBackend) WatchProjectRollbackRoute(app, deployment string, generation int64, expires time.Time, record func(context.Context, time.Time, time.Time) (bool, error)) {
	now := time.Now()
	if !expires.After(now) {
		return
	}
	watch := &projectRollbackWatch{generation: generation, deployment: deployment, appliedAt: now, expiresAt: expires, record: record}
	for {
		old, loaded := b.projectRollbackWatches.LoadOrStore(app, watch)
		if !loaded {
			break
		}
		if old.(*projectRollbackWatch).generation >= generation {
			return
		}
		if b.projectRollbackWatches.CompareAndSwap(app, old, watch) {
			break
		}
	}
	time.AfterFunc(time.Until(expires), func() { b.projectRollbackWatches.CompareAndDelete(app, watch) })
}

func (b *PGBackend) observeProjectRollbackResponse(ctx context.Context, app, deployment string, startedAt, succeededAt time.Time) {
	current, ok := b.projectRollbackWatches.Load(app)
	if !ok {
		return
	}
	watch := current.(*projectRollbackWatch)
	if deployment != watch.deployment || startedAt.Before(watch.appliedAt) || !succeededAt.Before(watch.expiresAt) {
		return
	}
	watch.mu.Lock()
	if watch.done || watch.writing || succeededAt.Before(watch.nextTry) {
		watch.mu.Unlock()
		return
	}
	watch.writing = true
	watch.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		recorded, _ := watch.record(ctx, startedAt, succeededAt)
		watch.mu.Lock()
		watch.writing, watch.done = false, recorded
		if !recorded {
			watch.nextTry = time.Now().Add(time.Second)
		}
		watch.mu.Unlock()
	}()
}
