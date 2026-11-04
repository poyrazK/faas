package state

import (
	"context"
	"time"
)

// OutboundAIQualityAlert is one drift outboundd found (ADR 20261021171712).
// Rates are basis points and evaluation_score is hundredths of a point;
// the counts are the calls each value was taken over.
type OutboundAIQualityAlert struct {
	ID            string
	IntegrationID string
	UsageDate     string
	Model         string
	Metric        string
	CurrentValue  int64
	BaselineValue int64
	CurrentCount  int64
	BaselineCount int64
	DetectedAt    time.Time
}

// OutboundAIQualityStore reads drift alerts and lets a person rate a
// logged call (ADR 20261021171712).
type OutboundAIQualityStore interface {
	// ListOutboundAIQualityAlerts lists an integration's alerts, newest
	// first.
	ListOutboundAIQualityAlerts(ctx context.Context, accountID, integrationID string, limit int) ([]OutboundAIQualityAlert, error)
	// SetOutboundAIRequestFeedback rates a logged call of any caller; a
	// later rating replaces the earlier one, and the call's daily rollup
	// row counts the change.
	SetOutboundAIRequestFeedback(ctx context.Context, accountID, integrationID, requestID, rating, label string) error
}

// OutboundAICallStats is one model's latency and errors over an
// integration's latest logged calls (ADR 20261022145950). The
// percentiles are nil when no call reported the value.
type OutboundAICallStats struct {
	Model         string
	Calls         int64
	ErrorCalls    int64
	TTFBP50MS     *int64
	TTFBP95MS     *int64
	DurationP50MS *int64
	DurationP95MS *int64
}

// OutboundAICallStatsStore reads latency and error rate from the request
// log for the dashboard (ADR 20261022145950).
type OutboundAICallStatsStore interface {
	// GetOutboundAICallStats summarises the integration's latest
	// OutboundAICallStatsCallsMax logged calls of the last
	// OutboundAICallStatsWindowHours hours by model, busiest first, and
	// reports how many calls it read.
	GetOutboundAICallStats(ctx context.Context, accountID, integrationID string) ([]OutboundAICallStats, int64, error)
}
