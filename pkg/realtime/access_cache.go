package realtime

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Access decisions are cached per node (ADR-1113, amendment of 2026-11-02).
// Every connection still checks its access every second, but against this
// cache: a principal's access and a ticket's claims are read from the control
// plane once, then kept until apid's access change feed names them, for at
// most realtimeAccessCacheTTL. While the feed is unavailable an entry lasts
// realtimeAccessRefreshInterval, as every read did before. A failed read keeps
// the last answer for realtimeAccessStaleMax, so an apid restart or a
// database blip does not close every socket.
const (
	realtimeAccessCacheTTL         = time.Minute
	realtimeAccessFeedPollInterval = time.Second
	realtimeAccessFeedHealthyFor   = 3 * time.Second
	realtimeAccessStaleMax         = 2 * time.Minute
	realtimeAccessRetryAfterError  = 5 * time.Second
	realtimeAccessCacheMaxEntries  = 65536
	// realtimeTicketMissTTL keeps a token the control plane does not know: a
	// ticket never comes back once expired or revoked.
	realtimeTicketMissTTL = 30 * time.Second
)

// errTicketCheckBusy refuses a new ticket connection whose ticket would need
// a control-plane read past the node's ticket read budget.
var errTicketCheckBusy = errors.New("realtime: ticket check busy")

// ManagedRealtimeAccessChangeReader polls apid's access change feed.
type ManagedRealtimeAccessChangeReader interface {
	ReadAccessChanges(ctx context.Context, epoch string, after int64) (state.ManagedRealtimeAccessChanges, error)
}

type accessKey struct{ endpointID, principal string }

type ticketKey struct {
	endpointID string
	hash       [sha256.Size]byte
}

type accessFlight[V any] struct {
	done  chan struct{}
	value V
	err   error
}

type accessEntry[V any] struct {
	value     V
	missing   bool // a definitive "not found" (tickets)
	fetchedAt time.Time
	failedAt  time.Time
	flight    *accessFlight[V]
	principal string // tickets: the principal whose changes invalidate it
}

type realtimeAccessCache struct {
	now func() time.Time

	mu        sync.Mutex
	gen       uint64
	feedEpoch string
	feedSeq   int64
	feedOK    time.Time
	// validAfter marks every entry fetched before it stale: the feed reset
	// and may have missed changes. Stale entries still answer through an
	// outage.
	validAfter time.Time
	access     map[accessKey]*accessEntry[state.ManagedRealtimeAccess]
	tickets    map[ticketKey]*accessEntry[state.RealtimeSubscriptionTicket]
	byPrinc    map[accessKey]map[ticketKey]struct{}

	ticketNode      *rate.Limiter
	ticketEndpoints map[string]*rate.Limiter
}

func newRealtimeAccessCache() *realtimeAccessCache {
	return &realtimeAccessCache{
		now:             time.Now,
		access:          map[accessKey]*accessEntry[state.ManagedRealtimeAccess]{},
		tickets:         map[ticketKey]*accessEntry[state.RealtimeSubscriptionTicket]{},
		byPrinc:         map[accessKey]map[ticketKey]struct{}{},
		ticketNode:      rate.NewLimiter(rate.Limit(api.RealtimeTicketReadsPerNodePerSecond), api.RealtimeTicketReadBurstPerNode),
		ticketEndpoints: map[string]*rate.Limiter{},
	}
}

func (c *realtimeAccessCache) feedHealthyLocked(now time.Time) bool {
	return !c.feedOK.IsZero() && now.Sub(c.feedOK) < realtimeAccessFeedHealthyFor
}

func freshLocked[V any](c *realtimeAccessCache, entry *accessEntry[V], now time.Time) bool {
	if entry.fetchedAt.IsZero() {
		return false
	}
	age := now.Sub(entry.fetchedAt)
	if entry.missing {
		return age < realtimeTicketMissTTL
	}
	if entry.fetchedAt.Before(c.validAfter) {
		return false
	}
	if c.feedHealthyLocked(now) {
		return age < realtimeAccessCacheTTL
	}
	return age < realtimeAccessRefreshInterval
}

// cachedRead serves key from entries, or reads it once for every caller
// waiting on it. A definitive answer (a value, or state.ErrNotFound) is
// cached unless the key was invalidated while it was read; an unavailable
// control plane leaves the last answer in place for realtimeAccessStaleMax
// and is not asked again for realtimeAccessRetryAfterError.
func cachedRead[K comparable, V any](ctx context.Context, c *realtimeAccessCache, entries map[K]*accessEntry[V], key K, admit func() error, read func(context.Context) (V, error), stored func(K, *accessEntry[V])) (V, error) {
	var zero V
	c.mu.Lock()
	now := c.now()
	entry := entries[key]
	if entry != nil && entry.flight != nil {
		flight := entry.flight
		c.mu.Unlock()
		select {
		case <-flight.done:
			return flight.value, flight.err
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}
	if entry != nil && (freshLocked(c, entry, now) || (!entry.failedAt.IsZero() && now.Sub(entry.failedAt) < realtimeAccessRetryAfterError)) {
		value, err := entry.answer(now)
		c.mu.Unlock()
		return value, err
	}
	if entry == nil {
		if admit != nil {
			if err := admit(); err != nil {
				c.mu.Unlock()
				return zero, err
			}
		}
		c.pruneLocked(len(entries), now)
		entry = &accessEntry[V]{}
		entries[key] = entry
	}
	flight := &accessFlight[V]{done: make(chan struct{})}
	entry.flight = flight
	gen := c.gen
	c.mu.Unlock()

	// The read outlives the caller that started it: other callers wait on it.
	value, err := read(context.WithoutCancel(ctx))

	c.mu.Lock()
	defer c.mu.Unlock()
	now = c.now()
	entry.flight = nil
	if err == nil || errors.Is(err, state.ErrNotFound) {
		if c.gen == gen && entries[key] == entry {
			entry.value, entry.missing, entry.fetchedAt, entry.failedAt = value, err != nil, now, time.Time{}
			if stored != nil {
				stored(key, entry)
			}
		}
	} else {
		entry.failedAt = now
		if last, lastErr := entry.answer(now); lastErr == nil || errors.Is(lastErr, state.ErrNotFound) {
			value, err = last, lastErr
		} else {
			value = zero
		}
		if entry.fetchedAt.IsZero() && entries[key] == entry {
			delete(entries, key)
		}
	}
	flight.value, flight.err = value, err
	close(flight.done)
	return value, err
}

// answer is the entry's last definitive answer while it is not too stale to
// serve through an outage.
func (e *accessEntry[V]) answer(now time.Time) (V, error) {
	var zero V
	if e.fetchedAt.IsZero() || now.Sub(e.fetchedAt) >= realtimeAccessStaleMax {
		return zero, errors.New("realtime: access check unavailable")
	}
	if e.missing {
		return zero, state.ErrNotFound
	}
	return e.value, nil
}

// pruneLocked drops expired entries when a map reaches its bound.
func (c *realtimeAccessCache) pruneLocked(size int, now time.Time) {
	if size < realtimeAccessCacheMaxEntries {
		return
	}
	c.dropStaleLocked(now, true)
}

func (c *realtimeAccessCache) dropStaleLocked(now time.Time, all bool) {
	for key, entry := range c.access {
		if entry.flight == nil && (all || now.Sub(entry.fetchedAt) >= realtimeAccessStaleMax) {
			delete(c.access, key)
		}
	}
	for key, entry := range c.tickets {
		if entry.flight == nil && (all || now.Sub(entry.fetchedAt) >= realtimeAccessStaleMax) {
			c.dropTicketLocked(key, entry)
		}
	}
}

func (c *realtimeAccessCache) dropTicketLocked(key ticketKey, entry *accessEntry[state.RealtimeSubscriptionTicket]) {
	delete(c.tickets, key)
	owner := accessKey{key.endpointID, entry.principal}
	if set := c.byPrinc[owner]; set != nil {
		delete(set, key)
		if len(set) == 0 {
			delete(c.byPrinc, owner)
		}
	}
}

// invalidateLocked drops what the cache knows about one principal of an
// endpoint, its access and every ticket it holds: a changed principal is not
// served from a last answer.
func (c *realtimeAccessCache) invalidateLocked(key accessKey) {
	c.gen++
	delete(c.access, key)
	for ticket := range c.byPrinc[key] {
		delete(c.tickets, ticket)
	}
	delete(c.byPrinc, key)
}

// invalidateAllLocked marks every entry stale without dropping it, so a
// node whose feed reset still has last answers to serve through an outage.
func (c *realtimeAccessCache) invalidateAllLocked() {
	c.gen++
	c.validAfter = c.now()
}

// applyFeed applies one page of apid's access change feed and reports
// whether more is waiting.
func (c *realtimeAccessCache) applyFeed(page state.ManagedRealtimeAccessChanges) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if page.Reset || page.Epoch != c.feedEpoch {
		c.invalidateAllLocked()
	} else {
		for _, change := range page.Changes {
			c.invalidateLocked(accessKey{change.EndpointID, change.Principal})
		}
	}
	c.feedEpoch, c.feedSeq, c.feedOK = page.Epoch, page.Seq, c.now()
	return page.More
}

func (c *realtimeAccessCache) feedPosition() (string, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.feedEpoch, c.feedSeq
}

// takeTicketRead admits one control-plane read of a ticket a new connection
// presents. The ticket's token is unauthenticated until read, so reads are
// bounded per endpoint and per node.
func (c *realtimeAccessCache) takeTicketReadLocked(endpointID string) error {
	limiter := c.ticketEndpoints[endpointID]
	if limiter == nil {
		if len(c.ticketEndpoints) >= realtimeAccessCacheMaxEntries {
			clear(c.ticketEndpoints)
		}
		limiter = rate.NewLimiter(rate.Limit(api.RealtimeTicketReadsPerEndpointPerSecond), api.RealtimeTicketReadBurstPerEndpoint)
		c.ticketEndpoints[endpointID] = limiter
	}
	now := c.now()
	if limiter.TokensAt(now) < 1 || !c.ticketNode.AllowN(now, 1) {
		return errTicketCheckBusy
	}
	limiter.AllowN(now, 1)
	return nil
}

// cachedAccess is the principal's access, read through the node cache.
func (m *Manager) cachedAccess(ctx context.Context, endpointID, principal string) (state.ManagedRealtimeAccess, error) {
	if m.cfg.AccessReader == nil {
		return state.ManagedRealtimeAccess{}, errors.New("realtime: access reader unavailable")
	}
	read := func(ctx context.Context) (state.ManagedRealtimeAccess, error) {
		readCtx, cancel := context.WithTimeout(ctx, realtimeAccessReadTimeout)
		defer cancel()
		return m.cfg.AccessReader.ReadRealtimeAccess(readCtx, endpointID, principal)
	}
	c := m.accessCache
	if c == nil {
		return read(ctx)
	}
	return cachedRead(ctx, c, c.access, accessKey{endpointID, principal}, nil, read, nil)
}

// cachedTicket is a ticket's claims, read through the node cache. A new
// connection (admission) is refused a malformed token without a read, and a
// read beyond the node's ticket read budget with errTicketCheckBusy.
func (m *Manager) cachedTicket(ctx context.Context, endpointID, token string, admission bool) (state.RealtimeSubscriptionTicket, error) {
	if !state.ValidRealtimeTicketToken(token) {
		return state.RealtimeSubscriptionTicket{}, state.ErrNotFound
	}
	c := m.accessCache
	if c == nil {
		claims, err := m.readTicket(ctx, endpointID, token)
		if err == nil && !time.Now().Before(claims.ExpiresAt) {
			return state.RealtimeSubscriptionTicket{}, state.ErrNotFound
		}
		return claims, err
	}
	key := ticketKey{endpointID: endpointID, hash: sha256.Sum256([]byte(token))}
	var admit func() error
	if admission {
		admit = func() error { return c.takeTicketReadLocked(endpointID) }
	}
	claims, err := cachedRead(ctx, c, c.tickets, key, admit, func(ctx context.Context) (state.RealtimeSubscriptionTicket, error) {
		return m.readTicket(ctx, endpointID, token)
	}, func(key ticketKey, entry *accessEntry[state.RealtimeSubscriptionTicket]) {
		if entry.missing {
			return
		}
		entry.principal = entry.value.Principal
		owner := accessKey{endpointID, entry.principal}
		if c.byPrinc[owner] == nil {
			c.byPrinc[owner] = map[ticketKey]struct{}{}
		}
		c.byPrinc[owner][key] = struct{}{}
	})
	// A cached ticket outlives its expiry by up to the cache's lifetime.
	if err == nil && !c.now().Before(claims.ExpiresAt) {
		return state.RealtimeSubscriptionTicket{}, state.ErrNotFound
	}
	return claims, err
}

// startAccessFeed polls apid's access change feed when the node's reader
// offers it.
func (m *Manager) startAccessFeed() {
	reader, ok := m.cfg.AccessReader.(ManagedRealtimeAccessChangeReader)
	if !ok {
		reader, ok = m.cfg.TicketReader.(ManagedRealtimeAccessChangeReader)
	}
	if !ok {
		return
	}
	go m.runAccessFeed(m.ctx, reader) //nolint:contextcheck // the Manager owns the poll loop's lifecycle; Close cancels it.
}

func (m *Manager) runAccessFeed(ctx context.Context, reader ManagedRealtimeAccessChangeReader) {
	ticker := time.NewTicker(realtimeAccessFeedPollInterval)
	defer ticker.Stop()
	lastPrune := time.Now()
	for {
		for more := true; more && ctx.Err() == nil; {
			epoch, seq := m.accessCache.feedPosition()
			pollCtx, cancel := context.WithTimeout(ctx, realtimeAccessReadTimeout)
			page, err := reader.ReadAccessChanges(pollCtx, epoch, seq)
			cancel()
			if err != nil {
				// The feed is unhealthy until a poll succeeds: entries fall
				// back to realtimeAccessRefreshInterval.
				break
			}
			more = m.accessCache.applyFeed(page)
		}
		if time.Since(lastPrune) >= realtimeAccessCacheTTL {
			m.accessCache.mu.Lock()
			m.accessCache.dropStaleLocked(m.accessCache.now(), false)
			m.accessCache.mu.Unlock()
			lastPrune = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
