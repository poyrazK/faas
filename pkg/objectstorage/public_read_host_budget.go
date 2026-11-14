package objectstorage

import (
	"context"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/time/rate"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/knownhosts"
)

// HostLookupBudget bounds the public-read matcher's store reads for hostnames
// it has never seen name an app (ADR-20261103163954 §4). The matcher runs in
// front of every GET and HEAD, before the gateway's own route-miss guard, and
// its mount cache keeps an answer for only publicMountTTL, so a flood of
// random names under the apps wildcard was a flood of control-plane reads.
// A host that named an app skips the budget; any other spends a token, and
// past the budget the request is answered 503 with Retry-After instead of
// reading the store.
type HostLookupBudget struct {
	mu             sync.Mutex
	known          map[string]struct{}
	maxKnown       int
	budget         *rate.Limiter
	wildcardBudget *rate.Limiter
	shed           prometheus.Counter
	// preloaded holds every app slug and verified custom domain, refreshed
	// from the store, so a real app's host skips the budget even before this
	// edge has served it (after a restart). Set before serving.
	preloaded *knownhosts.Set
}

// Preload lets every host in known skip the budget. Call it before the
// budget is used.
func (b *HostLookupBudget) Preload(known *knownhosts.Set) {
	if b != nil {
		b.preloaded = known
	}
}

// NewHostLookupBudget returns a budget whose shed counter is named
// <prefix>_host_lookups_shed_total.
func NewHostLookupBudget(prefix string) *HostLookupBudget {
	return &HostLookupBudget{
		known:          map[string]struct{}{},
		maxKnown:       api.PublicReadKnownHostsMax,
		budget:         rate.NewLimiter(rate.Limit(api.PublicReadNewHostLookupsPerSecond), api.PublicReadNewHostLookupBurst),
		wildcardBudget: rate.NewLimiter(rate.Limit(api.PublicReadWildcardRecoveryPerSecond), api.PublicReadWildcardRecoveryBurst),
		shed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: prefix + "_host_lookups_shed_total",
			Help: "Public-read host lookups refused with 503 because the edge spent its budget of store reads for hostnames it has never seen name an app.",
		}),
	}
}

// Collectors returns the budget's metrics for registration.
func (b *HostLookupBudget) Collectors() []prometheus.Collector {
	return []prometheus.Collector{b.shed}
}

// admit reports whether a store read of host may go ahead. A nil budget
// admits every read.
func (b *HostLookupBudget) admit(ctx context.Context, host string) bool {
	if b == nil || b.preloaded.Has(host) {
		return true
	}
	b.mu.Lock()
	_, seen := b.known[host]
	b.mu.Unlock()
	if seen || b.budget.Allow() || b.preloaded.Recover(ctx, host) || (b.preloaded.WildcardCandidate(host) && b.wildcardBudget.Allow()) {
		return true
	}
	b.shed.Inc()
	return false
}

// found records that host names an app: it skips the budget from now on. A
// full set is dropped whole; a flood of distinct real hosts is what fills it.
func (b *HostLookupBudget) found(host string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.known[host]; ok {
		return
	}
	if len(b.known) >= b.maxKnown {
		b.known = map[string]struct{}{}
	}
	b.known[host] = struct{}{}
}
