package logsearch

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ReadLimiter admits a bounded number of log reads per account and in all.
// A ledger read holds one of apid's database connections for up to
// api.LogSearchStatementTimeout, so unbounded concurrent searches, facets,
// log widgets and log pages could hold the whole pool and stall every other
// request. A read that finds no slot within its wait fails with
// state.ErrLogReadBusy.
type ReadLimiter struct {
	perAccount, max int
	wait            time.Duration

	mu       sync.Mutex
	inFlight map[string]int
	total    int
	freed    chan struct{} // closed and replaced whenever a slot frees
}

// NewReadLimiter admits perAccount reads of one account and max in all,
// waiting up to wait for a slot.
func NewReadLimiter(perAccount, max int, wait time.Duration) *ReadLimiter {
	return &ReadLimiter{perAccount: perAccount, max: max, wait: wait, inFlight: make(map[string]int), freed: make(chan struct{})}
}

func (l *ReadLimiter) acquire(ctx context.Context, account string) (func(), error) {
	timer := time.NewTimer(l.wait)
	defer timer.Stop()
	for {
		l.mu.Lock()
		if l.inFlight[account] < l.perAccount && l.total < l.max {
			l.inFlight[account]++
			l.total++
			l.mu.Unlock()
			return func() { l.release(account) }, nil
		}
		freed := l.freed
		l.mu.Unlock()
		select {
		case <-freed:
		case <-timer.C:
			return nil, state.ErrLogReadBusy
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (l *ReadLimiter) release(account string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight[account]--; l.inFlight[account] <= 0 {
		delete(l.inFlight, account)
	}
	l.total--
	close(l.freed)
	l.freed = make(chan struct{})
}

// Limit returns events whose reads take a slot of l; writes pass through.
func (l *ReadLimiter) Limit(events state.LogEventStore) state.LogEventStore {
	return limitedLogEvents{LogEventStore: events, limiter: l}
}

type limitedLogEvents struct {
	state.LogEventStore
	limiter *ReadLimiter
}

func (l limitedLogEvents) ListLogEvents(ctx context.Context, filter state.LogEventFilter) ([]state.LogEvent, bool, error) {
	release, err := l.limiter.acquire(ctx, filter.AccountID)
	if err != nil {
		return nil, false, err
	}
	defer release()
	return l.LogEventStore.ListLogEvents(ctx, filter)
}

func (l limitedLogEvents) ListAccountTraceLogEvents(ctx context.Context, filter state.AccountTraceLogFilter) ([]state.LogEvent, bool, error) {
	release, err := l.limiter.acquire(ctx, filter.AccountID)
	if err != nil {
		return nil, false, err
	}
	defer release()
	return l.LogEventStore.ListAccountTraceLogEvents(ctx, filter)
}

func (l limitedLogEvents) FacetLogEvents(ctx context.Context, filter state.LogEventFilter, routeLimit int) (state.LogEventFacets, error) {
	release, err := l.limiter.acquire(ctx, filter.AccountID)
	if err != nil {
		return state.LogEventFacets{}, err
	}
	defer release()
	return l.LogEventStore.FacetLogEvents(ctx, filter, routeLimit)
}
