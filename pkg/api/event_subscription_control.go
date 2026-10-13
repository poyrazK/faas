package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type EventSubscriptionResumeRequest struct {
	RatePerSecond *int `json:"rate_per_second,omitempty"`
}

func (r EventSubscriptionResumeRequest) Rate() (int, error) {
	rate := EventSubscriptionDrainRateDefault
	if r.RatePerSecond != nil {
		rate = *r.RatePerSecond
	}
	if rate < 0 || rate > EventSubscriptionDrainRateMax {
		return 0, fmt.Errorf("rate_per_second must be between 0 and %d; zero removes pacing", EventSubscriptionDrainRateMax)
	}
	return rate, nil
}

// EventSubscriptionCapacityGuard compares reviewed saved configuration and
// plan allowance; it does not reserve live capacity.
type EventSubscriptionCapacityGuard struct {
	MaxPendingDeliveries     *int `json:"max_pending_deliveries"`
	PlanMaxPendingDeliveries *int `json:"plan_max_pending_deliveries"`
}

// Zero restores the current plan limit. Positive values lower a subscription's
// live pending-plus-dispatching delivery ceiling without changing siblings.
type EventSubscriptionCapacityRequest struct {
	CommandID            string                          `json:"command_id,omitempty"`
	Expected             *EventSubscriptionCapacityGuard `json:"expected,omitempty"`
	MaxPendingDeliveries *int                            `json:"max_pending_deliveries"`
}

func (r EventSubscriptionCapacityRequest) Validate() error {
	if r.MaxPendingDeliveries == nil || *r.MaxPendingDeliveries < 0 {
		return fmt.Errorf("max_pending_deliveries must be explicit and nonnegative")
	}
	if e := r.Expected; e != nil && (e.MaxPendingDeliveries == nil || e.PlanMaxPendingDeliveries == nil || *e.MaxPendingDeliveries < 0 || *e.PlanMaxPendingDeliveries <= 0) {
		return fmt.Errorf("expected requires a nonnegative saved setting and a positive plan ceiling")
	}
	if r.CommandID != "" {
		id := r.CommandID
		if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
			return fmt.Errorf("command_id must be a nonzero hyphenated UUID")
		}
		raw, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
		nonzero := false
		for _, value := range raw {
			nonzero = nonzero || value != 0
		}
		if err != nil || len(raw) != 16 || !nonzero {
			return fmt.Errorf("command_id must be a nonzero hyphenated UUID")
		}
	}
	return nil
}

type EventSubscriptionCapacityCommandReceipt struct {
	CommandID                     string                          `json:"command_id"`
	AppID                         string                          `json:"app_id"`
	SubscriptionID                string                          `json:"subscription_id"`
	RequestedMaxPendingDeliveries int                             `json:"requested_max_pending_deliveries"`
	Expected                      *EventSubscriptionCapacityGuard `json:"expected,omitempty"`
	PreviousMaxPendingDeliveries  int                             `json:"previous_max_pending_deliveries"`
	PlanMaxPendingDeliveries      int                             `json:"plan_max_pending_deliveries"`
	EffectiveMaxPendingDeliveries int                             `json:"effective_max_pending_deliveries"`
	Outcome                       string                          `json:"outcome"`
	RecordedAt                    time.Time                       `json:"recorded_at"`
	RetainUntil                   time.Time                       `json:"retain_until"`
}

type EventSubscriptionDeliveryControl struct {
	CapacityCommandReceipt        *EventSubscriptionCapacityCommandReceipt `json:"capacity_command_receipt,omitempty"`
	MaxPendingDeliveries          int                                      `json:"max_pending_deliveries"`
	EffectiveMaxPendingDeliveries int                                      `json:"effective_max_pending_deliveries"`
	LiveDeliveries                int64                                    `json:"live_deliveries"`

	CircuitBreaker       *EventCircuitBreakerResponse `json:"circuit_breaker,omitempty"`
	SubscriptionID       string                       `json:"subscription_id"`
	AppID                string                       `json:"app_id"`
	Paused               bool                         `json:"paused"`
	RatePerSecond        int                          `json:"rate_per_second"`
	PendingRecipients    int64                        `json:"pending_recipients"`
	ProcessingRecipients int64                        `json:"processing_recipients"`
	OldestPendingAt      *time.Time                   `json:"oldest_pending_at,omitempty"`
	OldestAgeSeconds     float64                      `json:"oldest_age_seconds"`
	UpdatedAt            *time.Time                   `json:"updated_at,omitempty"`
}

func subscriptionDeliveryControlPath(app, id string) string {
	return "/v1/apps/" + url.PathEscape(app) + "/event-subscriptions/" + url.PathEscape(id) + "/delivery-control"
}
func (c *Client) GetEventSubscriptionDeliveryControl(ctx context.Context, app, id string) (EventSubscriptionDeliveryControl, error) {
	var out EventSubscriptionDeliveryControl
	err := c.do(ctx, http.MethodGet, subscriptionDeliveryControlPath(app, id), nil, &out)
	return out, err
}
func (c *Client) PauseEventSubscription(ctx context.Context, app, id string) (EventSubscriptionDeliveryControl, error) {
	var out EventSubscriptionDeliveryControl
	err := c.do(ctx, http.MethodPost, subscriptionDeliveryControlPath(app, id)+"/pause", nil, &out)
	return out, err
}
func (c *Client) ResumeEventSubscription(ctx context.Context, app, id string, req EventSubscriptionResumeRequest) (EventSubscriptionDeliveryControl, error) {
	var out EventSubscriptionDeliveryControl
	err := c.do(ctx, http.MethodPost, subscriptionDeliveryControlPath(app, id)+"/resume", req, &out)
	return out, err
}

func (c *Client) SetEventSubscriptionCapacity(ctx context.Context, app, id string, req EventSubscriptionCapacityRequest) (EventSubscriptionDeliveryControl, error) {
	var out EventSubscriptionDeliveryControl
	err := c.do(ctx, http.MethodPut, subscriptionDeliveryControlPath(app, id)+"/capacity", req, &out)
	return out, err
}
