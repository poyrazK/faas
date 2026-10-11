package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

type ManagedRealtimeEventSchemaRequest struct {
	Schema json.RawMessage `json:"schema"`
}
type ManagedRealtimeEventSchemaResponse struct {
	Channel   string          `json:"channel"`
	EventType string          `json:"event_type"`
	Version   int             `json:"version"`
	Schema    json.RawMessage `json:"schema"`
	CreatedAt time.Time       `json:"created_at"`
}

func eventSchemaAPIPath(slug, ep, ch, event string, version int) string {
	return fmt.Sprintf("/v1/apps/%s/realtime/endpoints/%s/channels/%s/schemas/%s/%d", url.PathEscape(slug), url.PathEscape(ep), url.PathEscape(ch), url.PathEscape(event), version)
}
func (c *Client) PutManagedRealtimeEventSchema(ctx context.Context, slug, ep, ch, event string, version int, req ManagedRealtimeEventSchemaRequest) (ManagedRealtimeEventSchemaResponse, error) {
	var out ManagedRealtimeEventSchemaResponse
	err := c.do(ctx, "PUT", eventSchemaAPIPath(slug, ep, ch, event, version), req, &out)
	return out, err
}
func (c *Client) GetManagedRealtimeEventSchema(ctx context.Context, slug, ep, ch, event string, version int) (ManagedRealtimeEventSchemaResponse, error) {
	var out ManagedRealtimeEventSchemaResponse
	err := c.do(ctx, "GET", eventSchemaAPIPath(slug, ep, ch, event, version), nil, &out)
	return out, err
}
