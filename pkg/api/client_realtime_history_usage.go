package api

import (
	"context"
	"net/http"
)

func (c *Client) GetManagedRealtimeHistoryUsage(ctx context.Context) (ManagedRealtimeHistoryUsageResponse, error) {
	var out ManagedRealtimeHistoryUsageResponse
	err := c.do(ctx, http.MethodGet, "/v1/account/realtime-history-usage", nil, &out)
	return out, err
}
