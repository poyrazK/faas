package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

type ManagedRealtimeReducerRequest struct {
	Sequence int64           `json:"sequence"`
	Entities json.RawMessage `json:"entities"`
}
type ManagedRealtimeReducerResponse struct {
	EntityExpirations map[string]time.Time `json:"entity_expirations,omitempty"`
	EntityVersions    map[string]int64     `json:"entity_versions"`
	Channel           string               `json:"channel"`
	Sequence          int64                `json:"sequence"`
	Entities          json.RawMessage      `json:"entities"`
	UpdatedAt         time.Time            `json:"updated_at"`
}

func reducerAPIPath(slug, ep, ch string) string {
	return fmt.Sprintf("/v1/apps/%s/realtime/endpoints/%s/channels/%s/reducer", url.PathEscape(slug), url.PathEscape(ep), url.PathEscape(ch))
}
func (c *Client) PutManagedRealtimeReducer(ctx context.Context, slug, ep, ch string, req ManagedRealtimeReducerRequest) (ManagedRealtimeReducerResponse, error) {
	var out ManagedRealtimeReducerResponse
	err := c.do(ctx, "PUT", reducerAPIPath(slug, ep, ch), req, &out)
	return out, err
}
func (c *Client) GetManagedRealtimeReducer(ctx context.Context, slug, ep, ch string) (ManagedRealtimeReducerResponse, error) {
	var out ManagedRealtimeReducerResponse
	err := c.do(ctx, "GET", reducerAPIPath(slug, ep, ch), nil, &out)
	return out, err
}
func (c *Client) DeleteManagedRealtimeReducer(ctx context.Context, slug, ep, ch string) error {
	var out any
	return c.do(ctx, "DELETE", reducerAPIPath(slug, ep, ch), nil, &out)
}
