package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"

	"github.com/google/uuid"
)

func (r EventRecoveryNotificationRetryRequest) Validate() error {
	validID := func(id string) bool {
		parsed, err := uuid.Parse(id)
		return err == nil && parsed != uuid.Nil && parsed.String() == id
	}
	if !validID(r.RequestID) || len(r.Targets) < 1 || len(r.Targets) > EventRecoveryNotificationRetryTargetsMax {
		return fmt.Errorf("request_id must be a canonical nonzero UUID and targets must contain 1 to %d receivers", EventRecoveryNotificationRetryTargetsMax)
	}
	seen := map[string]bool{}
	for _, t := range r.Targets {
		if (t.Kind != "admission" && t.Kind != "execution") || !validID(t.WebhookID) || !validID(t.DeliveryID) || t.ExpectedReplayGeneration == nil || *t.ExpectedReplayGeneration < 0 || *t.ExpectedReplayGeneration >= math.MaxInt32 || seen[t.DeliveryID] {
			return fmt.Errorf("each target requires a notification kind, distinct delivery UUID, webhook UUID, and valid expected_replay_generation")
		}
		seen[t.DeliveryID] = true
	}
	return nil
}
func (r EventRecoveryNotificationRetryRequest) Canonical() EventRecoveryNotificationRetryRequest {
	out := r
	out.Targets = make([]EventRecoveryNotificationRetryTarget, len(r.Targets))
	for i, t := range r.Targets {
		out.Targets[i] = t
		if t.ExpectedReplayGeneration != nil {
			n := *t.ExpectedReplayGeneration
			out.Targets[i].ExpectedReplayGeneration = &n
		}
	}
	sort.Slice(out.Targets, func(i, j int) bool { return out.Targets[i].DeliveryID < out.Targets[j].DeliveryID })
	return out
}

func (c *Client) PreviewEventRecoveryNotificationRetry(ctx context.Context, id string) (EventRecoveryNotificationRetryPreview, error) {
	var out EventRecoveryNotificationRetryPreview
	err := c.do(ctx, http.MethodGet, "/v1/event-recoveries/"+url.PathEscape(id)+"/notifications/retry-preview", nil, &out)
	return out, err
}
func (c *Client) RetryEventRecoveryNotifications(ctx context.Context, id string, req EventRecoveryNotificationRetryRequest) (EventRecoveryNotificationRetryResponse, error) {
	var out EventRecoveryNotificationRetryResponse
	if err := req.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, "/v1/event-recoveries/"+url.PathEscape(id)+"/notifications/retry", req, &out)
	return out, err
}
