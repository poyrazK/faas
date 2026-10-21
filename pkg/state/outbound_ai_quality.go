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
