package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// EventRoutingRetryPolicy applies before invocation admission. Duration bounds
// routing attempt time plus scheduled retry delays, excluding admission waits.
// MaxDeliveryAgeMS separately bounds wall-clock age from platform acceptance.
type EventRoutingRetryPolicy struct {
	MaxDeliveryAgeMS   int64 `json:"max_delivery_age_ms,omitempty"`
	MaxAttempts        int   `json:"max_attempts" yaml:"max_attempts" toml:"max_attempts"`
	MaxRetryDurationMS int64 `json:"max_retry_duration_ms"`
	InitialBackoffMS   int64 `json:"initial_backoff_ms"`
	MaxBackoffMS       int64 `json:"max_backoff_ms"`
	Jitter             bool  `json:"jitter"`
}

func DefaultEventRoutingRetryPolicy() EventRoutingRetryPolicy {
	return EventRoutingRetryPolicy{MaxAttempts: EventRoutingRetryDefaultAttempts, InitialBackoffMS: EventRoutingRetryDefaultInitialMS, MaxBackoffMS: EventRoutingRetryDefaultMaxMS}
}
func (p EventRoutingRetryPolicy) Validate() error {
	if p.MaxDeliveryAgeMS < 0 || p.MaxDeliveryAgeMS > EventDeliveryAgeMaxMS {
		return fmt.Errorf("max_delivery_age_ms must be between 0 and %d; zero disables expiry", EventDeliveryAgeMaxMS)
	}
	if p.MaxAttempts < 1 || p.MaxAttempts > EventRoutingRetryMaxAttempts {
		return fmt.Errorf("max_attempts must be between 1 and %d", EventRoutingRetryMaxAttempts)
	}
	if p.InitialBackoffMS < 1 || p.MaxBackoffMS < p.InitialBackoffMS || p.MaxBackoffMS > EventRoutingRetryMaxBackoffMS {
		return fmt.Errorf("backoff must satisfy 1 <= initial_backoff_ms <= max_backoff_ms <= %d", EventRoutingRetryMaxBackoffMS)
	}
	if p.MaxRetryDurationMS < 0 || p.MaxRetryDurationMS > EventRoutingRetryMaxDurationMS {
		return fmt.Errorf("max_retry_duration_ms must be between 0 and %d; zero disables this bound", EventRoutingRetryMaxDurationMS)
	}
	return nil
}

type EventRoutingRetryPolicyResponse struct {
	SubscriptionID string                  `json:"subscription_id"`
	Configured     bool                    `json:"configured"`
	Policy         EventRoutingRetryPolicy `json:"policy"`
}

func eventRoutingRetryPolicyPath(app, id string) string {
	return "/v1/apps/" + url.PathEscape(app) + "/event-subscriptions/" + url.PathEscape(id) + "/retry-policy"
}
func (c *Client) GetEventSubscriptionRetryPolicy(ctx context.Context, app, id string) (EventRoutingRetryPolicyResponse, error) {
	var out EventRoutingRetryPolicyResponse
	err := c.do(ctx, http.MethodGet, eventRoutingRetryPolicyPath(app, id), nil, &out)
	return out, err
}
func (c *Client) SetEventSubscriptionRetryPolicy(ctx context.Context, app, id string, p EventRoutingRetryPolicy) (EventRoutingRetryPolicyResponse, error) {
	var out EventRoutingRetryPolicyResponse
	err := c.do(ctx, http.MethodPut, eventRoutingRetryPolicyPath(app, id), p, &out)
	return out, err
}
func (c *Client) ResetEventSubscriptionRetryPolicy(ctx context.Context, app, id string) (EventRoutingRetryPolicyResponse, error) {
	var out EventRoutingRetryPolicyResponse
	err := c.do(ctx, http.MethodDelete, eventRoutingRetryPolicyPath(app, id), nil, &out)
	return out, err
}
