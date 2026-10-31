package objectstorage

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/singleflight"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// OrgAccessSnapshotStore reads an organization membership without locks.
type OrgAccessSnapshotStore interface {
	OrgAccessSnapshot(ctx context.Context, orgID, accountID string) (state.OrgAccessSnapshot, error)
}

// OrgAccessCache checks a credential's organization access fence against
// cached membership revisions, so a read under that credential needs no
// transaction (ADR-20261030075301 step 3). A revision is cached for
// ObjectReadAdmissionTTL and, while the database is unreachable, served for up
// to ObjectReadAdmissionMaxStale: a removal or role change reaches reads
// within 2 s, or 30 s during an outage. Writes keep the locking fence.
type OrgAccessCache struct {
	store OrgAccessSnapshotStore
	now   func() time.Time
	group singleflight.Group

	mu      sync.Mutex
	entries map[orgAccessKey]orgAccessEntry

	checks *prometheus.CounterVec
}

type orgAccessKey struct{ org, account string }

type orgAccessEntry struct {
	member    bool
	revision  int64
	fetchedAt time.Time
}

func NewOrgAccessCache(store OrgAccessSnapshotStore, now func() time.Time) *OrgAccessCache {
	if now == nil {
		now = time.Now
	}
	return &OrgAccessCache{
		store: store, now: now, entries: map[orgAccessKey]orgAccessEntry{},
		checks: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "s3_gateway_org_access_checks_total", Help: "Organization access fence checks for reads by source: cached, refreshed, stale (served while the database is unreachable) or failed."}, []string{"source"}),
	}
}

// Collectors are the cache's metrics, for the gateway's registry.
func (c *OrgAccessCache) Collectors() []prometheus.Collector {
	return []prometheus.Collector{c.checks}
}

// Check returns nil while the fence's account is a member of its organization
// at the fence's revision, state.ErrOrgAccessRevoked once it is not, and
// state.ErrObjectUsageStale when the membership cannot be read.
func (c *OrgAccessCache) Check(ctx context.Context, fence state.OrgAccessFence) error {
	e, err := c.entry(ctx, orgAccessKey{org: fence.OrgID, account: fence.AccountID})
	if err != nil {
		return err
	}
	if !e.member || e.revision != fence.Revision {
		return state.ErrOrgAccessRevoked
	}
	return nil
}

func (c *OrgAccessCache) entry(ctx context.Context, key orgAccessKey) (orgAccessEntry, error) {
	now := c.now()
	c.mu.Lock()
	cached, ok := c.entries[key]
	c.mu.Unlock()
	if ok && now.Sub(cached.fetchedAt) < api.ObjectReadAdmissionTTL {
		c.checks.WithLabelValues("cached").Inc()
		return cached, nil
	}
	v, err, _ := c.group.Do(key.org+"/"+key.account, func() (any, error) {
		// A caller that missed just before another refresh finished uses it.
		c.mu.Lock()
		if fresh, ok := c.entries[key]; ok && now.Sub(fresh.fetchedAt) < api.ObjectReadAdmissionTTL {
			c.mu.Unlock()
			return fresh, nil
		}
		c.mu.Unlock()
		// A detached read: one caller's cancellation must not fail the others.
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectReadAdmissionTimeout)
		defer cancel()
		snapshot, err := c.store.OrgAccessSnapshot(readCtx, key.org, key.account)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return nil, err
		}
		fresh := orgAccessEntry{member: err == nil, revision: snapshot.Revision, fetchedAt: now}
		c.mu.Lock()
		c.storeLocked(key, fresh, now)
		c.mu.Unlock()
		return fresh, nil
	})
	if err != nil {
		if ok && now.Sub(cached.fetchedAt) < api.ObjectReadAdmissionMaxStale {
			c.checks.WithLabelValues("stale").Inc()
			return cached, nil
		}
		c.checks.WithLabelValues("failed").Inc()
		return orgAccessEntry{}, state.ErrObjectUsageStale
	}
	c.checks.WithLabelValues("refreshed").Inc()
	return v.(orgAccessEntry), nil
}

// storeLocked caches a membership, first dropping expired ones when the cache
// is full; one that still does not fit is used once, not cached.
func (c *OrgAccessCache) storeLocked(key orgAccessKey, e orgAccessEntry, now time.Time) {
	if _, ok := c.entries[key]; !ok && len(c.entries) >= api.MaxObjectReadAdmissionEntries {
		for k, old := range c.entries {
			if now.Sub(old.fetchedAt) >= api.ObjectReadAdmissionMaxStale {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= api.MaxObjectReadAdmissionEntries {
			return
		}
	}
	c.entries[key] = e
}
