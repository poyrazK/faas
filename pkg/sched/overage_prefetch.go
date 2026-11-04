package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// reaperSpendCapMaxAge is how old a spend-cap status the reaper may act on
// (ADR 20261103164725). meterd prices usage a minute at a time, so a status
// read every 10 s tick, as the reaper's per-account Check did once the 5 s
// cache had expired, mostly re-read figures that had not moved. The wake gate
// keeps the checker's own 5 s freshness.
const reaperSpendCapMaxAge = 30 * time.Second

// overagePrefetcher lets the reaper read a tick's spend-cap statuses in bulk
// and accept a status up to maxAge old.
type overagePrefetcher interface {
	Prefetch(ctx context.Context, accountIDs []string, maxAge time.Duration)
	CheckWithin(ctx context.Context, accountID string, maxAge time.Duration) (OverageStatus, int64, int64, error)
}

// prefetchSpendCaps reads the tick's billing subjects' caps in one query when
// the checker can.
func (e *Engine) prefetchSpendCaps(ctx context.Context, ownerIDs []string) {
	if p, ok := e.overage.(overagePrefetcher); ok && len(ownerIDs) > 0 {
		p.Prefetch(ctx, ownerIDs, reaperSpendCapMaxAge)
	}
}

// reaperAppMustPark is appMustPark for the reaper's sweep: it accepts a
// spend-cap status up to reaperSpendCapMaxAge old. The park it leads to,
// ParkApp, checks appMustPark again with the checker's own freshness before
// it touches an instance.
func (e *Engine) reaperAppMustPark(ctx context.Context, app state.App, account state.Account) bool {
	if app.Status == state.AppEvictedCold || account.Status == state.AccountSuspended || account.AbuseHeld() {
		return true
	}
	ownerID := account.ResourceOwnerID()
	if p, ok := e.overage.(overagePrefetcher); ok && ownerID != "" {
		status, _, _, _ := p.CheckWithin(ctx, ownerID, reaperSpendCapMaxAge)
		return status == OverageReached
	}
	return e.spendCapReached(ctx, ownerID)
}

// fetchedWithin reports accountID's cached entry when it was read within
// maxAge. Every entry is stored with exp = read time + ttl.
func (c *memCacheOverageChecker) fetchedWithin(accountID string, now time.Time, maxAge time.Duration) (overageEntry, bool) {
	c.mu.RLock()
	e, ok := c.cache[accountID]
	c.mu.RUnlock()
	return e, ok && now.Sub(e.exp.Add(-c.ttl)) < maxAge
}

// CheckWithin is Check accepting a cached status read within maxAge.
func (c *memCacheOverageChecker) CheckWithin(ctx context.Context, accountID string, maxAge time.Duration) (OverageStatus, int64, int64, error) {
	now := c.now()
	if e, ok := c.fetchedWithin(accountID, now, maxAge); ok {
		return e.status, e.observedCents, e.capCents, nil
	}
	return c.load(ctx, accountID, now)
}

// Prefetch reads, in one query, the caps of the accounts among accountIDs
// whose status is older than maxAge, and caches the uncapped ones as OK: an
// account without a cap needs no month total. A capped account is left for
// CheckWithin to load. A store without state.OverageCapsReader, or a failed
// read, leaves every account to CheckWithin.
func (c *memCacheOverageChecker) Prefetch(ctx context.Context, accountIDs []string, maxAge time.Duration) {
	reader, ok := c.store.(state.OverageCapsReader)
	if !ok {
		return
	}
	now := c.now()
	seen := make(map[string]struct{}, len(accountIDs))
	stale := make([]string, 0, len(accountIDs))
	for _, id := range accountIDs {
		if _, dup := seen[id]; dup || id == "" {
			continue
		}
		seen[id] = struct{}{}
		if _, fresh := c.fetchedWithin(id, now, maxAge); !fresh {
			stale = append(stale, id)
		}
	}
	if len(stale) == 0 {
		return
	}
	caps, err := reader.AccountOverageCaps(ctx, stale)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range stale {
		if _, capped := caps[id]; capped {
			continue
		}
		c.cache[id] = overageEntry{status: OverageOK, exp: now.Add(c.ttl), lastEmittedDay: c.cache[id].lastEmittedDay}
	}
}
