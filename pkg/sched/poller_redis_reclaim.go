package sched

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Walk a bounded PEL page, including consumers retired with a deleted trigger.
// XCLAIM rechecks the idle window atomically. XPENDING + XCLAIM also works on
// Redis 5; no XAUTOCLAIM or newer server version is required.
func (r *redisPoller) reclaimIdle(ctx context.Context, limit int64) ([]redis.XMessage, error) {
	r.mu.Lock()
	start := r.reclaimCursor
	r.mu.Unlock()
	if start == "" {
		start = "-"
	}
	pending, err := r.client.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: r.stream, Group: r.group, Start: start, End: "+", Count: limit + 1,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("redis_poller: pending reclaim page: %w", err)
	}
	var ids []string
	for _, entry := range pending {
		if entry.ID != start && entry.Consumer != r.consumer && entry.Idle >= r.visibility && int64(len(ids)) < limit {
			ids = append(ids, entry.ID)
		}
	}
	r.mu.Lock()
	r.reclaimCursor = ""
	if int64(len(pending)) == limit+1 {
		r.reclaimCursor = pending[len(pending)-1].ID
	}
	r.mu.Unlock()
	if len(ids) == 0 {
		return nil, nil
	}
	entries, err := r.client.XClaim(ctx, &redis.XClaimArgs{
		Stream: r.stream, Group: r.group, Consumer: r.consumer, MinIdle: r.visibility, Messages: ids,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("redis_poller: reclaim idle entries: %w", err)
	}
	return entries, nil
}

// Keep active handlers from appearing abandoned. The ownership check and
// refresh are atomic, so renewal cannot steal a claim another consumer owns.
func (r *redisPoller) keepLeases(ctx context.Context, ids []string) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	refresh := redis.NewScript(`
local renewed = 0
for i = 3, #ARGV do
  local pending = redis.call('XPENDING', KEYS[1], ARGV[1], ARGV[i], ARGV[i], 1)
  if #pending == 1 and pending[1][2] == ARGV[2] then
    redis.call('XCLAIM', KEYS[1], ARGV[1], ARGV[2], 0, ARGV[i], 'JUSTID')
    renewed = renewed + 1
  end
end
return renewed`)
	go func() {
		defer close(done)
		ticker := time.NewTicker(max(time.Millisecond, r.visibility/3))
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				args := []any{r.group, r.consumer}
				r.mu.Lock()
				for _, id := range ids {
					if _, ok := r.inFlight[id]; ok {
						args = append(args, id)
					}
				}
				r.mu.Unlock()
				if len(args) > 2 {
					_ = refresh.Run(ctx, r.client, []string{r.stream}, args...).Err()
				}
			}
		}
	}()
	return func() { cancel(); <-done }
}
