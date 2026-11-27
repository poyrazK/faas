package state

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// An account whose compute plan was activated again this month (a plan change
// or a new price version) bills its month in segments split at each
// activation's hour, so the spend cap reads the account's usage before that
// hour (MonthBilledComputeMBSeconds). Summing it from the minute rows on every
// read cost one scan of every minute since the activation: on production
// 2026-11-19, after the 2026-11-08 overage price version, each Scale account's
// spend-cap read summed eleven days of rows, 55 ms and ~35,000 buffer pages a
// call, 25% of the control plane's database time (meterd's quota tick, schedd's
// wake gate and the dashboard all read it).
//
// Usage before an hour stops changing once meterd can no longer write any
// minute before it: a minute's compute usage is written at most
// api.MeterUsageFinalAfter after the minute starts (ADR-1162, the bound the
// provider pusher already trusts before it delivers an hour). From then on
// the figure is summed once per process and kept.

// finalUsageBeforeCacheMax bounds the cache. It holds one entry per account
// and activation hour of the current month, so the bound is never reached in
// practice; reaching it only drops the entries.
const finalUsageBeforeCacheMax = 4096

type finalUsageBeforeKey struct {
	account string
	at      int64
}

// finalUsageBeforeCache keeps final usage sums for the current UTC month. A
// nil cache keeps nothing.
type finalUsageBeforeCache struct {
	mu    sync.Mutex
	month time.Time
	sums  map[finalUsageBeforeKey]int64
}

func newFinalUsageBeforeCache() *finalUsageBeforeCache {
	return &finalUsageBeforeCache{}
}

func (c *finalUsageBeforeCache) get(month time.Time, account string, at time.Time) (int64, bool) {
	if c == nil {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.month.Equal(month) {
		return 0, false
	}
	sum, ok := c.sums[finalUsageBeforeKey{account: account, at: at.Unix()}]
	return sum, ok
}

func (c *finalUsageBeforeCache) put(month time.Time, account string, at time.Time, sum int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.month.Equal(month) || len(c.sums) >= finalUsageBeforeCacheMax {
		c.month = month
		c.sums = make(map[finalUsageBeforeKey]int64)
	}
	c.sums[finalUsageBeforeKey{account: account, at: at.Unix()}] = sum
}

// usageFinalBefore reports whether no compute usage can still be written for
// any minute before at, as of now.
func usageFinalBefore(at, now time.Time) bool {
	return !now.Before(at.Add(api.MeterUsageFinalAfter))
}

// finalUsageBefore returns accountID's compute usage in [monthStart, at),
// which the caller has checked is final (usageFinalBefore). The first read in
// a process sums the minute rows; later reads return the kept sum.
func (s *PgStore) finalUsageBefore(ctx context.Context, accountID string, monthStart, at time.Time) (int64, error) {
	if sum, ok := s.finalUsage.get(monthStart, accountID, at); ok {
		return sum, nil
	}
	var sum int64
	if err := s.pool.QueryRow(ctx,
		`select COALESCE(SUM(mb_seconds), 0)::bigint
		   from usage_minutes
		  where account_id = $1 and minute >= $2 and minute < $3`,
		accountID, monthStart, at).Scan(&sum); err != nil {
		return 0, fmt.Errorf("final usage before %s: %w", at.Format(time.RFC3339), err)
	}
	s.finalUsage.put(monthStart, accountID, at, sum)
	return sum, nil
}
