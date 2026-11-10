package object

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// inventoryDueFunc reads up to limit due buckets in due order, leaving out
// skip.
type inventoryDueFunc func(ctx context.Context, limit int32, skip []string) ([]state.ObjectInventoryDue, error)

// inventoryQueue hands due buckets to the sweep's workers one at a time, as
// each finishes, instead of in batches that wait for their slowest listing
// (ADR-20261110151628 §1). It reads the due list a page at a time and never
// hands out a bucket twice in one sweep. At most
// api.ObjectStorageInventoryLongListingWorkers long listings run at once.
// Once the sweep's deadline passes it starts no long listing, and hands out
// short buckets only while an attempt that started before the deadline is
// still in flight, so the sweep never outlasts its longest attempt but its
// workers do not idle meanwhile. After a store read fails it hands out
// nothing more.
type inventoryQueue struct {
	due      inventoryDueFunc
	deadline time.Time

	mu          sync.Mutex
	wake        *sync.Cond
	short, long []state.ObjectBucket
	seen        map[string]bool
	longActive  int
	early       int  // attempts handed out before the deadline, still in flight
	drained     bool // nothing is due that this sweep has not taken
	expired     bool
	err         error
}

// inventoryTake is one bucket handed to a worker, returned to done.
type inventoryTake struct {
	bucket      state.ObjectBucket
	long, early bool
}

func newInventoryQueue(due inventoryDueFunc, deadline time.Time) *inventoryQueue {
	q := &inventoryQueue{due: due, deadline: deadline, seen: map[string]bool{}}
	q.wake = sync.NewCond(&q.mu)
	return q
}

// run starts the deadline and cancellation wake-ups for workers waiting on a
// long-listing slot. The returned stop ends them.
func (q *inventoryQueue) run(ctx context.Context) (stop func()) {
	expire := func() {
		q.mu.Lock()
		q.expired = true
		q.wake.Broadcast()
		q.mu.Unlock()
	}
	timer := time.AfterFunc(time.Until(q.deadline), expire)
	stopCtx := context.AfterFunc(ctx, expire)
	return func() {
		timer.Stop()
		stopCtx()
	}
}

// next returns the next bucket to inventory. It reports false when the sweep
// should stop taking work.
func (q *inventoryQueue) next(ctx context.Context) (inventoryTake, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for {
		if q.err != nil || ctx.Err() != nil {
			return inventoryTake{}, false
		}
		if q.expired || !time.Now().Before(q.deadline) {
			if q.early == 0 {
				return inventoryTake{}, false
			}
			if len(q.short) > 0 {
				return q.takeLocked(false, false), true
			}
			if !q.drained {
				q.refillLocked(ctx)
				continue
			}
			return inventoryTake{}, false
		}
		if len(q.long) > 0 && q.longActive < api.ObjectStorageInventoryLongListingWorkers {
			return q.takeLocked(true, true), true
		}
		if len(q.short) > 0 {
			return q.takeLocked(false, true), true
		}
		if !q.drained {
			q.refillLocked(ctx)
			continue
		}
		if len(q.long) == 0 {
			return inventoryTake{}, false
		}
		// Only long listings remain and their slots are taken.
		q.wake.Wait()
	}
}

func (q *inventoryQueue) takeLocked(long, early bool) inventoryTake {
	t := inventoryTake{long: long, early: early}
	if long {
		t.bucket, q.long = q.long[0], q.long[1:]
		q.longActive++
	} else {
		t.bucket, q.short = q.short[0], q.short[1:]
	}
	if early {
		q.early++
	}
	return t
}

// done returns a finished take's long-listing slot.
func (q *inventoryQueue) done(t inventoryTake) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if t.long {
		q.longActive--
	}
	if t.early {
		q.early--
	}
	q.wake.Broadcast()
}

// fail stops the queue with a store or configuration error.
func (q *inventoryQueue) fail(err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err == nil {
		q.err = err
	}
	q.wake.Broadcast()
}

func (q *inventoryQueue) refillLocked(ctx context.Context) {
	skip := make([]string, 0, len(q.seen))
	for id := range q.seen {
		skip = append(skip, id)
	}
	rows, err := q.due(ctx, api.ObjectStorageInventorySweepBatch, skip)
	if err != nil {
		q.err = err
		q.wake.Broadcast()
		return
	}
	added := 0
	for _, r := range rows {
		if q.seen[r.Bucket.ID] {
			continue
		}
		q.seen[r.Bucket.ID] = true
		added++
		if r.Long {
			q.long = append(q.long, r.Bucket)
		} else {
			q.short = append(q.short, r.Bucket)
		}
	}
	// A short page is the end of the due list. A store without a skip list
	// returns buckets this sweep already took (they did not advance their
	// claim); stop there rather than spin.
	if added == 0 || len(rows) < api.ObjectStorageInventorySweepBatch {
		q.drained = true
	}
}

// pending reports whether due work was left when the queue stopped.
func (q *inventoryQueue) pending() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.short) > 0 || len(q.long) > 0 || !q.drained
}

func (q *inventoryQueue) failure() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.err
}

// inventoryDue reads the store's sweep queue, or adapts DueObjectInventories
// for a store without one (no skip list, no long listings).
func inventoryDue(st state.ObjectStorageAccountingStore) inventoryDueFunc {
	if queue, ok := st.(state.ObjectInventoryQueueStore); ok {
		return queue.DueObjectInventoryQueue
	}
	return func(ctx context.Context, limit int32, _ []string) ([]state.ObjectInventoryDue, error) {
		rows, err := st.DueObjectInventories(ctx, limit)
		out := make([]state.ObjectInventoryDue, 0, len(rows))
		for _, b := range rows {
			out = append(out, state.ObjectInventoryDue{Bucket: b})
		}
		return out, err
	}
}
