package api

import (
	"context"
	"net/http"
	"time"
)

// EventSubscriptionPendingSummary covers all retained captured pending application
// recipients in one read-only observation. Counts are not sampled or modeled.
type EventSubscriptionPendingSummary struct {
	Coverage               string                     `json:"coverage"`
	AppID                  string                     `json:"app_id"`
	SubscriptionID         string                     `json:"subscription_id"`
	ObservedAt             time.Time                  `json:"observed_at"`
	ReadOnly               bool                       `json:"read_only"`
	Complete               bool                       `json:"complete"`
	PendingRecipients      int64                      `json:"pending_recipients"`
	DeadlineRecipients     int64                      `json:"deadline_recipients"`
	AlreadyExpiredCount    int64                      `json:"already_expired_count"`
	EarliestDeadlineAt     *time.Time                 `json:"earliest_deadline_at,omitempty"`
	Current                EventSubscriptionRateLimit `json:"current"`
	Paused                 bool                       `json:"paused"`
	LegacyRatePerSecond    int                        `json:"legacy_rate_per_second"`
	CircuitState           string                     `json:"circuit_state"`
	CapacityBlockingScopes []string                   `json:"capacity_blocking_scopes"`
	Constraints            []string                   `json:"constraints"`
}

func (c *Client) GetEventSubscriptionPendingSummary(ctx context.Context, app, sub string) (EventSubscriptionPendingSummary, error) {
	var out EventSubscriptionPendingSummary
	err := c.do(ctx, http.MethodGet, subscriptionDeliveryControlPath(app, sub)+"/pending-summary", nil, &out)
	return out, err
}
