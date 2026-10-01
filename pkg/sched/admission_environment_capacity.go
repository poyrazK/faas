package sched

import "github.com/onebox-faas/faas/pkg/api"

// Each deployed environment has its own configured serving ceiling. The
// application's aggregate plan budget remains shared across its environments.
type servingCapacity struct {
	concurrency, sharedConcurrency int
	limit, sharedLimit             int
}

func (c servingCapacity) refusal() (limit, observed int, refused bool) {
	if c.concurrency >= c.limit {
		return c.limit, c.concurrency, true
	}
	if c.sharedConcurrency >= c.sharedLimit {
		return c.sharedLimit, c.sharedConcurrency, true
	}
	return c.limit, c.concurrency, false
}

func (c servingCapacity) fullAfterAdmission() bool {
	return c.concurrency+1 >= c.limit || c.sharedConcurrency+1 >= c.sharedLimit
}

func (l *NodeLedger) servingEnvironmentConcurrencyLocked(appID, key string, production bool) int {
	if production || key == "" {
		return l.perAppProduction[appID]
	}
	return l.perAppEnvironment[appID+"\x00"+key]
}

func (l *NodeLedger) servingEnvironmentConcurrency(appID, key string, production bool) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ensureEnvironmentConcurrencyLocked()
	return l.servingEnvironmentConcurrencyLocked(appID, key, production)
}

func (l *NodeLedger) servingCapacityLocked(appID, key string, production bool, configuredLimit int, limits api.Limits, allowOverlap bool) servingCapacity {
	if configuredLimit <= 0 || configuredLimit > limits.MaxConcurrency {
		configuredLimit = limits.MaxConcurrency
	}
	capacity := servingCapacity{
		concurrency:       l.servingEnvironmentConcurrencyLocked(appID, key, production),
		sharedConcurrency: l.perApp[appID], limit: configuredLimit, sharedLimit: limits.MaxConcurrency,
	}
	// Overlap is a new revision of a serving environment. A sibling stage
	// cannot supply that prerequisite or grant itself extra plan capacity.
	if allowOverlap && capacity.concurrency > 0 {
		capacity.limit += api.RolloutConcurrencyGrant
		capacity.sharedLimit += api.RolloutConcurrencyGrant
	}
	return capacity
}

func (l *NodeLedger) servingCapacity(appID, key string, production bool, configuredLimit int, limits api.Limits, allowOverlap bool) servingCapacity {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ensureEnvironmentConcurrencyLocked()
	return l.servingCapacityLocked(appID, key, production, configuredLimit, limits, allowOverlap)
}
