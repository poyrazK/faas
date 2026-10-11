package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
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

type EventSubscriptionDeliveryControl struct {
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
