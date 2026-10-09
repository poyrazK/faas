package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// AppPublishEventRequest uses a stable application-scoped producer key.
type AppPublishEventRequest struct {
	Key           string          `json:"key"`
	Type          string          `json:"type"`
	Data          json.RawMessage `json:"data"`
	Time          *time.Time      `json:"time,omitempty"`
	SchemaVersion string          `json:"schemaversion,omitempty"`
}

func (r AppPublishEventRequest) Validate() error {
	if len(r.Key) == 0 || len(r.Key) > AppEventPublishKeyMaxBytes {
		return fmt.Errorf("key must contain 1..%d printable ASCII bytes without spaces", AppEventPublishKeyMaxBytes)
	}
	for _, b := range []byte(r.Key) {
		if b < 0x21 || b > 0x7e {
			return fmt.Errorf("key must contain printable ASCII without spaces")
		}
	}
	if len(r.Data) == 0 || !json.Valid(r.Data) {
		return fmt.Errorf("data must be a JSON value")
	}
	return nil
}

type AppPublishEventResponse struct {
	AppID     string               `json:"app_id"`
	Source    string               `json:"source"`
	Duplicate bool                 `json:"duplicate"`
	Receipt   PublishEventResponse `json:"receipt"`
}

func (c *Client) PublishAppEvent(ctx context.Context, slug string, req AppPublishEventRequest) (AppPublishEventResponse, error) {
	var out AppPublishEventResponse
	if err := req.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/events:publish", req, &out)
	return out, err
}
