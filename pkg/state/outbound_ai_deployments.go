package state

import (
	"context"
	"sort"
	"time"
)

// OutboundAIDeploymentStats is one deployment's AI calls over a window
// (ADR 20261022153155): its first and latest UTC minute with a call, the
// calls with a provider error or a quality flag, cost and tokens, and the
// calls with a first byte with their summed time to first byte.
type OutboundAIDeploymentStats struct {
	DeploymentID   string
	FirstMinute    time.Time
	LastMinute     time.Time
	Calls          int64
	ErrorCalls     int64
	FlaggedCalls   int64
	CostMillicents int64
	InputTokens    int64
	OutputTokens   int64
	TTFBCalls      int64
	TTFBMSTotal    int64
}

// OutboundAIDeploymentStatsStore reads an app's AI calls by deployment
// (ADR 20261022153155).
type OutboundAIDeploymentStatsStore interface {
	// ListOutboundAIDeploymentStats lists the app's deployments with AI
	// calls at or after since, latest call first, at most limit.
	ListOutboundAIDeploymentStats(ctx context.Context, appID string, since time.Time, limit int) ([]OutboundAIDeploymentStats, error)
}

var _ OutboundAIDeploymentStatsStore = (*MemStore)(nil)

func (m *MemStore) ListOutboundAIDeploymentStats(_ context.Context, appID string, since time.Time, limit int) ([]OutboundAIDeploymentStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]OutboundAIDeploymentStats, 0)
	for _, stats := range m.outboundAIDeploymentStats[appID] {
		if !stats.LastMinute.Before(since) {
			out = append(out, stats)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].LastMinute.Equal(out[j].LastMinute) {
			return out[i].LastMinute.After(out[j].LastMinute)
		}
		return out[i].DeploymentID < out[j].DeploymentID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SeedOutboundAIDeploymentStats stands in for outboundd's settlement in
// tests.
func (m *MemStore) SeedOutboundAIDeploymentStats(appID string, stats ...OutboundAIDeploymentStats) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outboundAIDeploymentStats == nil {
		m.outboundAIDeploymentStats = make(map[string][]OutboundAIDeploymentStats)
	}
	m.outboundAIDeploymentStats[appID] = append(m.outboundAIDeploymentStats[appID], stats...)
}
