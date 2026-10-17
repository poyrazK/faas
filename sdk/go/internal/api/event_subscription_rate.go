package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// EventSubscriptionRateRequest is a guarded, idempotent token-bucket command.
type EventSubscriptionRateRequest struct {
	CommandID        string `json:"command_id"`
	ExpectedRevision *int64 `json:"expected_revision"`
	Enabled          *bool  `json:"enabled"`
	IntervalMillis   int64  `json:"interval_millis"`
	Burst            int64  `json:"burst"`
}

func (r EventSubscriptionRateRequest) Validate() error {
	zero := 0
	if r.Enabled == nil || r.CommandID == "" || (EventSubscriptionCapacityRequest{CommandID: r.CommandID, MaxPendingDeliveries: &zero}).Validate() != nil || r.ExpectedRevision == nil || *r.ExpectedRevision < 0 || r.IntervalMillis < EventSubscriptionRateIntervalMinMillis || r.IntervalMillis > EventSubscriptionRateIntervalMaxMillis || r.Burst < 1 || r.Burst > EventSubscriptionRateBurstMax {
		return fmt.Errorf("rate command requires a nonzero UUID, explicit nonnegative revision, interval 10..3600000 ms and burst 1..100")
	}
	return nil
}

type EventSubscriptionRateLimit struct {
	Enabled         bool       `json:"enabled"`
	Revision        int64      `json:"revision"`
	IntervalMillis  int64      `json:"interval_millis"`
	Burst           int64      `json:"burst"`
	AvailableTokens int64      `json:"available_tokens"`
	RateLimited     bool       `json:"rate_limited"`
	NextAvailableAt *time.Time `json:"next_available_at,omitempty"`
	ObservedAt      time.Time  `json:"observed_at"`
}
type EventSubscriptionRateCommandReceipt struct {
	CommandID      string                     `json:"command_id"`
	AppID          string                     `json:"app_id"`
	SubscriptionID string                     `json:"subscription_id"`
	Sequence       int64                      `json:"sequence"`
	Outcome        string                     `json:"outcome"`
	Result         EventSubscriptionRateLimit `json:"result"`
	RecordedAt     time.Time                  `json:"recorded_at"`
	RetainUntil    time.Time                  `json:"retain_until"`
}
type EventSubscriptionRateCommands struct {
	Receipts  []EventSubscriptionRateCommandReceipt `json:"receipts"`
	NextAfter int64                                 `json:"next_after,omitempty"`
}

func (c *Client) SetEventSubscriptionRateLimit(ctx context.Context, app, sub string, req EventSubscriptionRateRequest) (EventSubscriptionRateCommandReceipt, error) {
	var out EventSubscriptionRateCommandReceipt
	err := c.do(ctx, http.MethodPut, subscriptionDeliveryControlPath(app, sub)+"/rate-limit", req, &out)
	return out, err
}
func (c *Client) GetEventSubscriptionRateCommand(ctx context.Context, app, sub, command string) (EventSubscriptionRateCommandReceipt, error) {
	var out EventSubscriptionRateCommandReceipt
	err := c.do(ctx, http.MethodGet, subscriptionDeliveryControlPath(app, sub)+"/rate-limit/commands/"+url.PathEscape(command), nil, &out)
	return out, err
}
func (c *Client) ListEventSubscriptionRateCommands(ctx context.Context, app, sub string, after int64) (EventSubscriptionRateCommands, error) {
	var out EventSubscriptionRateCommands
	err := c.do(ctx, http.MethodGet, subscriptionDeliveryControlPath(app, sub)+fmt.Sprintf("/rate-limit/commands?after=%d", after), nil, &out)
	return out, err
}

// Independent of plan quotas.
const (
	EventSubscriptionRateIntervalMinMillis  int64 = 10
	EventSubscriptionRateIntervalMaxMillis  int64 = 3600000
	EventSubscriptionRateBurstMax           int64 = 100
	EventSubscriptionRateCommandRetention         = 30 * 24 * time.Hour
	EventSubscriptionRateCommandMaxReceipts       = 128
)

func subscriptionDeliveryControlPath(app, sub string) string {
	return "/v1/apps/" + url.PathEscape(app) + "/event-subscriptions/" + url.PathEscape(sub) + "/delivery-control"
}
