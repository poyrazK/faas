package api

import (
	"context"
	"net/http"
	"net/url"
)

// Recovery notification reporting is metadata-only and never captures events.

func (c *Client) GetEventRecoveryNotifications(ctx context.Context, job string) (EventRecoveryNotifications, error) {
	var out EventRecoveryNotifications
	err := c.do(ctx, http.MethodGet, "/v1/event-recoveries/"+url.PathEscape(job)+"/notifications", nil, &out)
	return out, err
}
