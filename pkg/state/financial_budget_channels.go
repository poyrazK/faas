package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Budget alert channel delivery outcomes (ADR-20261019193500). The failures
// double as the bounded last_error values the table accepts.
const (
	FinancialBudgetChannelDelivered   = "delivered"
	FinancialBudgetChannelFailed      = "channel_delivery_failed"
	FinancialBudgetChannelUnavailable = "channel_unavailable"
)

// FinancialBudgetChannelStore binds budgets to notification channels
// (ADR-749) and keeps one durable delivery per budget alert and channel.
// Deliveries are enqueued with each alert RecordFinancialBudgetEvaluation
// records; claims and settlements are fenced like the account email outbox.
// Optional on Store; callers type-assert.
type FinancialBudgetChannelStore interface {
	// SetFinancialBudgetChannels replaces a live policy's channels. Channels
	// the policy's account does not own are ignored, so callers validate.
	SetFinancialBudgetChannels(ctx context.Context, account, policy string, channelIDs []string) error
	// ListFinancialBudgetChannels returns a policy's channels by name, with
	// their sealed destinations.
	ListFinancialBudgetChannels(ctx context.Context, account, policy string) ([]NotificationChannel, error)
	ClaimFinancialBudgetAlertChannel(ctx context.Context, now time.Time) (FinancialBudgetAlertChannelDelivery, error)
	// SettleFinancialBudgetAlertChannel records a claimed delivery's outcome:
	// FinancialBudgetChannelDelivered or one of the failures. A failure is
	// retried with backoff until FinancialBudgetChannelAttemptsMax claims.
	SettleFinancialBudgetAlertChannel(ctx context.Context, alertID, channelID, token string, now time.Time, outcome string) error
}

// FinancialBudgetAlertChannelDelivery is one claimed delivery of a budget
// alert to a channel. Alert carries the current policy spec.
type FinancialBudgetAlertChannelDelivery struct {
	Alert         FinancialBudgetAlertDelivery
	Channel       NotificationChannel
	State         string
	Attempts      int
	LeaseToken    string
	NextAttemptAt time.Time
	DeliveredAt   *time.Time
	LastError     string
}

var (
	_ FinancialBudgetChannelStore = (*PgStore)(nil)
	_ FinancialBudgetChannelStore = (*MemStore)(nil)
)

func validFinancialBudgetChannelIDs(account, policy string, channelIDs []string) bool {
	if uuid.Validate(account) != nil || uuid.Validate(policy) != nil || len(channelIDs) > api.MaxChannelsPerFinancialBudget {
		return false
	}
	for _, id := range channelIDs {
		if uuid.Validate(id) != nil {
			return false
		}
	}
	return true
}

func validFinancialBudgetChannelOutcome(outcome string) bool {
	return outcome == FinancialBudgetChannelDelivered || outcome == FinancialBudgetChannelFailed || outcome == FinancialBudgetChannelUnavailable
}

// financialBudgetChannelSettlement is the next state of a delivery that was
// claimed attempts times.
func financialBudgetChannelSettlement(attempts int, now time.Time, outcome string) (state, lastError string, next time.Time, delivered *time.Time) {
	now = now.UTC()
	switch {
	case outcome == FinancialBudgetChannelDelivered:
		return "delivered", "", now, &now
	case attempts >= api.FinancialBudgetChannelAttemptsMax:
		return "abandoned", outcome, now, nil
	}
	return "retrying", outcome, now.Add(financialBudgetAlertRetry(attempts)), nil
}

func cloneFinancialBudgetAlertChannelDelivery(d FinancialBudgetAlertChannelDelivery) FinancialBudgetAlertChannelDelivery {
	d.Alert = cloneFinancialBudgetAlert(d.Alert)
	d.Channel.TargetSealed = append([]byte(nil), d.Channel.TargetSealed...)
	if d.DeliveredAt != nil {
		at := *d.DeliveredAt
		d.DeliveredAt = &at
	}
	return d
}
