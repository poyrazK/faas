package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// AppPublishEventRequest uses a stable application-scoped producer key.

func ValidateAppEventProducerKey(key string) error {
	if len(key) == 0 || len(key) > AppEventPublishKeyMaxBytes {
		return fmt.Errorf("key must contain 1..%d printable ASCII bytes without spaces", AppEventPublishKeyMaxBytes)
	}
	for _, b := range []byte(key) {
		if b < 0x21 || b > 0x7e {
			return fmt.Errorf("key must contain printable ASCII without spaces")
		}
	}
	return nil
}

func (r AppPublishEventRequest) Validate() error {
	if err := ValidateAppEventProducerKey(r.Key); err != nil {
		return err
	}
	if len(r.Data) == 0 || !json.Valid(r.Data) {
		return fmt.Errorf("data must be a JSON value")
	}
	return nil
}

func (c *Client) PublishAppEvent(ctx context.Context, slug string, req AppPublishEventRequest) (AppPublishEventResponse, error) {
	var out AppPublishEventResponse
	if err := req.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/events:publish", req, &out)
	return out, err
}
