package gateway

// Flag kill switch, gateway half (ADR 20261114150000 §3). Each guest request
// carries X-Faas-Flag-Kills: the flags killed in its deployment's project
// environment. The SDKs honor it before their cached bundle, so a kill takes
// effect on the next request instead of the next SDK refresh.
//
// The list is cached per deployment, filled by one database lookup and
// emptied whenever feature_flags_changed arrives. A missed notification is
// bounded by flagKillsTTL. A lookup that fails or exceeds flagKillsLookupBudget
// stamps nothing and is retried after flagKillsRetryAfter: the bundle still
// carries the kill, so the request is never delayed for it.

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/flags"
)

const (
	flagKillsTTL          = 30 * time.Second
	flagKillsRetryAfter   = 5 * time.Second
	flagKillsLookupBudget = 250 * time.Millisecond
	flagKillsMaxEntries   = 20000
)

// FlagKillLoader returns the killed flag keys of a deployment's environment.
// A deployment outside a project, or with no published flags, has none.
type FlagKillLoader func(ctx context.Context, deploymentID string) ([]string, error)

type flagKillEntry struct {
	header  string
	expires time.Time
}

// FlagKillCache is safe for concurrent use.
type FlagKillCache struct {
	load FlagKillLoader
	now  func() time.Time

	mu         sync.Mutex
	generation uint64
	entries    map[string]flagKillEntry
}

// NewFlagKillCache returns a cache over load; nil disables stamping.
func NewFlagKillCache(load FlagKillLoader) *FlagKillCache {
	return &FlagKillCache{load: load, now: time.Now, entries: map[string]flagKillEntry{}}
}

// Header returns the X-Faas-Flag-Kills value for deploymentID, "" for none.
func (c *FlagKillCache) Header(ctx context.Context, deploymentID string) string {
	if c == nil || c.load == nil || deploymentID == "" {
		return ""
	}
	now := c.now()
	c.mu.Lock()
	entry, ok := c.entries[deploymentID]
	generation := c.generation
	c.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.header
	}
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), flagKillsLookupBudget)
	keys, err := c.load(lookupCtx, deploymentID)
	cancel()
	entry = flagKillEntry{expires: now.Add(flagKillsTTL)}
	if err != nil {
		entry.expires = now.Add(flagKillsRetryAfter)
	} else {
		entry.header = flags.FormatKills(keys)
	}
	c.mu.Lock()
	// A notification that arrived during the lookup may make this result
	// stale; keep it for this request but do not cache it.
	if c.generation == generation {
		if len(c.entries) >= flagKillsMaxEntries {
			c.entries = map[string]flagKillEntry{}
		}
		c.entries[deploymentID] = entry
	}
	c.mu.Unlock()
	return entry.header
}

// InvalidateAll drops every cached kill list.
func (c *FlagKillCache) InvalidateAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.generation++
	c.entries = map[string]flagKillEntry{}
	c.mu.Unlock()
}

// WithFlagKillLoader enables X-Faas-Flag-Kills stamping.
func (b *PGBackend) WithFlagKillLoader(load FlagKillLoader) *PGBackend {
	b.flagKills = NewFlagKillCache(load)
	return b
}

// FlagKillsHeader implements flagKillSource.
func (b *PGBackend) FlagKillsHeader(ctx context.Context, deploymentID string) string {
	if b == nil {
		return ""
	}
	return b.flagKills.Header(ctx, deploymentID)
}

// InvalidateFlagKills is called on feature_flags_changed and after missed
// notifications.
func (b *PGBackend) InvalidateFlagKills() {
	if b != nil {
		b.flagKills.InvalidateAll()
		// Edge routes come from the same configuration (ADR-20261124212701).
		b.edgeFlags.InvalidateAll()
	}
}

// flagKillSource is the optional backend capability the handler stamps from.
type flagKillSource interface {
	FlagKillsHeader(ctx context.Context, deploymentID string) string
}
