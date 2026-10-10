package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// Stable problem codes for the ADR-749 notification channel endpoints.
const (
	CodeNotificationChannelInvalid = "notification_channel_invalid"
	CodeNotificationChannelLimit   = "notification_channel_limit_reached"
)

var notificationChannelNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

// CreateNotificationChannelRequest is the POST /v1/notification-channels
// body. Exactly the fields for Kind are used: slack_webhook_url for slack,
// pagerduty_routing_key (+ pagerduty_region, default us) for pagerduty,
// email for email.
type CreateNotificationChannelRequest struct {
	Name                string `json:"name"`
	Kind                string `json:"kind"`
	SlackWebhookURL     string `json:"slack_webhook_url,omitempty"`
	PagerDutyRoutingKey string `json:"pagerduty_routing_key,omitempty"`
	PagerDutyRegion     string `json:"pagerduty_region,omitempty"`
	Email               string `json:"email,omitempty"`
}

// NotificationChannelResponse is one channel. Target is a non-secret hint
// (Slack workspace and hook ids, the routing key's last four characters, or
// the email address); the sealed destination is never returned.
type NotificationChannelResponse struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Target          string `json:"target"`
	PagerDutyRegion string `json:"pagerduty_region,omitempty"`
	LastDeliveredAt string `json:"last_delivered_at,omitempty"`
	LastError       string `json:"last_error,omitempty"`
	LastErrorAt     string `json:"last_error_at,omitempty"`
	CreatedAt       string `json:"created_at"`
}

// TestNotificationChannelResponse reports a test send.
type TestNotificationChannelResponse struct {
	Delivered bool   `json:"delivered"`
	Error     string `json:"error,omitempty"`
}

// NormalizeNotificationChannelRequest checks the name and kind and that only
// the kind's own fields are set; destination formats are checked by
// pkg/alertchannels at the handler.
func NormalizeNotificationChannelRequest(req CreateNotificationChannelRequest) (CreateNotificationChannelRequest, *Problem) {
	req.Email = strings.TrimSpace(req.Email)
	if !notificationChannelNamePattern.MatchString(req.Name) {
		return req, ErrNotificationChannelInvalid("name must match [a-z][a-z0-9_-]{0,62}")
	}
	switch req.Kind {
	case "slack":
		if req.SlackWebhookURL == "" || req.PagerDutyRoutingKey != "" || req.PagerDutyRegion != "" || req.Email != "" {
			return req, ErrNotificationChannelInvalid("a slack channel takes slack_webhook_url only")
		}
	case "pagerduty":
		if req.PagerDutyRegion == "" {
			req.PagerDutyRegion = "us"
		}
		if req.PagerDutyRoutingKey == "" || req.SlackWebhookURL != "" || req.Email != "" {
			return req, ErrNotificationChannelInvalid("a pagerduty channel takes pagerduty_routing_key and optional pagerduty_region only")
		}
	case "email":
		if req.Email == "" || req.SlackWebhookURL != "" || req.PagerDutyRoutingKey != "" || req.PagerDutyRegion != "" {
			return req, ErrNotificationChannelInvalid("an email channel takes email only")
		}
	default:
		return req, ErrNotificationChannelInvalid("kind must be slack, pagerduty or email")
	}
	return req, nil
}

// ErrNotificationChannelInvalid is a 400 for a malformed channel.
func ErrNotificationChannelInvalid(reason string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeNotificationChannelInvalid, "Invalid notification channel", reason).
		WithDocs(docsBase + "/alerts#notification-channels")
}

// ErrNotificationChannelLimitReached is a 422 naming the per-account cap.
func ErrNotificationChannelLimitReached(observed int) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeNotificationChannelLimit, "Notification channel limit reached",
		fmt.Sprintf("an account can have at most %d notification channels; delete one to add another", MaxNotificationChannelsPerAccount)).
		WithLimit(int64(MaxNotificationChannelsPerAccount), int64(observed)).
		WithDocs(docsBase + "/alerts#notification-channels")
}

// ListNotificationChannels returns the account's channels (ADR-749).
func (c *Client) ListNotificationChannels(ctx context.Context) ([]NotificationChannelResponse, error) {
	var out []NotificationChannelResponse
	return out, c.do(ctx, "GET", "/v1/notification-channels", nil, &out)
}

// CreateNotificationChannel adds a Slack, PagerDuty or email channel.
func (c *Client) CreateNotificationChannel(ctx context.Context, req CreateNotificationChannelRequest) (NotificationChannelResponse, error) {
	var out NotificationChannelResponse
	return out, c.do(ctx, "POST", "/v1/notification-channels", req, &out)
}

// GetNotificationChannel fetches one channel.
func (c *Client) GetNotificationChannel(ctx context.Context, id string) (NotificationChannelResponse, error) {
	var out NotificationChannelResponse
	return out, c.do(ctx, "GET", "/v1/notification-channels/"+id, nil, &out)
}

// DeleteNotificationChannel removes one channel.
func (c *Client) DeleteNotificationChannel(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/notification-channels/"+id, nil, nil)
}

// TestNotificationChannel sends a marked test message through a channel.
func (c *Client) TestNotificationChannel(ctx context.Context, id string) (TestNotificationChannelResponse, error) {
	var out TestNotificationChannelResponse
	return out, c.do(ctx, "POST", "/v1/notification-channels/"+id+"/test", nil, &out)
}
