package api

import (
	"context"
	"fmt"
	"net/url"
)

// GetAppHealth reads the default-scope HTTP health evidence without waking the app.
func (c *Client) GetAppHealth(ctx context.Context, slug string) (AppHealthResponse, error) {
	var out AppHealthResponse
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/health", nil, &out)
	return out, err
}

func (c *Client) ListAppHealthHistory(ctx context.Context, slug string, limit int, before string) (AppHealthHistoryPage, error) {
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", fmt.Sprint(limit))
	}
	if before != "" {
		query.Set("before", before)
	}
	var out AppHealthHistoryPage
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/health/history?"+query.Encode(), nil, &out)
	return out, err
}
