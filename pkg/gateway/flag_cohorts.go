package gateway

// Analytics cohorts, gateway half (ADR 20261119102741 §4). Each guest
// request with a verified customer carries X-Faas-Flag-Cohorts: the cohorts
// of its deployment's environment that contain the customer, or "-" for
// none. SDKs read it as the gregale.cohort attribute.
//
// Entries are cached per deployment and customer, filled by one database
// lookup and emptied whenever analytics_cohorts_changed arrives. A missed
// notification is bounded by flagCohortsTTL. A lookup that fails or exceeds
// flagCohortsLookupBudget stamps nothing, so the attribute reads as missing
// and no cohort rule matches; it is retried after flagCohortsRetryAfter and
// never delays the request beyond the budget.

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/flags"
)

const (
	flagCohortsTTL          = 60 * time.Second
	flagCohortsRetryAfter   = 5 * time.Second
	flagCohortsLookupBudget = 100 * time.Millisecond
	flagCohortsMaxEntries   = 50000
)

// FlagCohortLoader returns the cohort keys of the deployment's environment
// that contain the customer.
type FlagCohortLoader func(ctx context.Context, deploymentID, customerID string) ([]string, error)

type flagCohortKey struct{ deployment, customer string }

type flagCohortEntry struct {
	header  string
	expires time.Time
}

// FlagCohortCache is safe for concurrent use.
type FlagCohortCache struct {
	load FlagCohortLoader
	now  func() time.Time

	mu         sync.Mutex
	generation uint64
	entries    map[flagCohortKey]flagCohortEntry
}

// NewFlagCohortCache returns a cache over load; nil disables stamping.
func NewFlagCohortCache(load FlagCohortLoader) *FlagCohortCache {
	return &FlagCohortCache{load: load, now: time.Now, entries: map[flagCohortKey]flagCohortEntry{}}
}

// Header returns the X-Faas-Flag-Cohorts value for the deployment and
// customer, or "" when it cannot say.
func (c *FlagCohortCache) Header(ctx context.Context, deploymentID, customerID string) string {
	if c == nil || c.load == nil || deploymentID == "" || customerID == "" {
		return ""
	}
	key := flagCohortKey{deploymentID, customerID}
	now := c.now()
	c.mu.Lock()
	entry, ok := c.entries[key]
	generation := c.generation
	c.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.header
	}
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), flagCohortsLookupBudget)
	keys, err := c.load(lookupCtx, deploymentID, customerID)
	cancel()
	entry = flagCohortEntry{expires: now.Add(flagCohortsTTL)}
	if err != nil {
		entry.expires = now.Add(flagCohortsRetryAfter)
	} else {
		entry.header = flags.FormatCohorts(keys)
	}
	c.mu.Lock()
	if c.generation == generation {
		if len(c.entries) >= flagCohortsMaxEntries {
			c.entries = map[flagCohortKey]flagCohortEntry{}
		}
		c.entries[key] = entry
	}
	c.mu.Unlock()
	return entry.header
}

// InvalidateAll drops every cached entry.
func (c *FlagCohortCache) InvalidateAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.generation++
	c.entries = map[flagCohortKey]flagCohortEntry{}
	c.mu.Unlock()
}

// WithFlagCohortLoader enables X-Faas-Flag-Cohorts stamping.
func (b *PGBackend) WithFlagCohortLoader(load FlagCohortLoader) *PGBackend {
	b.flagCohorts = NewFlagCohortCache(load)
	return b
}

// FlagCohortsHeader implements flagCohortSource.
func (b *PGBackend) FlagCohortsHeader(ctx context.Context, deploymentID, customerID string) string {
	if b == nil {
		return ""
	}
	return b.flagCohorts.Header(ctx, deploymentID, customerID)
}

// InvalidateFlagCohorts is called on analytics_cohorts_changed and after
// missed notifications.
func (b *PGBackend) InvalidateFlagCohorts() {
	if b != nil {
		b.flagCohorts.InvalidateAll()
	}
}

// flagCohortSource is the optional backend capability the handler stamps
// cohorts from.
type flagCohortSource interface {
	FlagCohortsHeader(ctx context.Context, deploymentID, customerID string) string
}
