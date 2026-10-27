package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const QueueEvidenceHandlerCompletion = "handler_completion"

// A deployment explicitly declares safe handling through this non-secret override.
const QueueCanaryCapabilityEnv = "GREGALE_QUEUE_CANARY_BINDINGS"

type CreateQueueBindingCanaryRequest struct {
	DeploymentID   string `json:"deployment_id"`
	Binding        string `json:"binding"`
	TTLSeconds     int    `json:"ttl_seconds"`
	PublishConsent bool   `json:"publish_consent"`
}
type QueueBindingCanary struct {
	ID           string     `json:"id"`
	Binding      string     `json:"binding"`
	DeploymentID string     `json:"deployment_id"`
	Scope        string     `json:"scope"`
	Status       string     `json:"status"`
	Reason       string     `json:"reason,omitempty"`
	PublishedAt  time.Time  `json:"published_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	DeliveredAt  *time.Time `json:"delivered_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

func (c *PreviewClient) CreateQueueBindingCanary(ctx context.Context, app string, req CreateQueueBindingCanaryRequest) (QueueBindingCanary, error) {
	var out QueueBindingCanary
	if !req.PublishConsent {
		return out, fmt.Errorf("explicit queue publication consent is required")
	}
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(app)+"/bindings/queue-canaries", req, &out)
	return out, err
}
func (c *PreviewClient) GetQueueBindingCanary(ctx context.Context, app, id string) (QueueBindingCanary, error) {
	var out QueueBindingCanary
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/bindings/queue-canaries/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *PreviewClient) CancelQueueBindingCanary(ctx context.Context, app, id string) (QueueBindingCanary, error) {
	var out QueueBindingCanary
	err := c.do(ctx, http.MethodDelete, "/v1/apps/"+url.PathEscape(app)+"/bindings/queue-canaries/"+url.PathEscape(id), nil, &out)
	return out, err
}
