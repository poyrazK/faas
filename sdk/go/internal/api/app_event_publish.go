package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// AppPublishEventRequest mirrors the application-scoped producer-key contract.
type AppPublishEventRequest struct {
	Key           string          `json:"key"`
	Type          string          `json:"type"`
	Data          json.RawMessage `json:"data"`
	Time          *time.Time      `json:"time,omitempty"`
	SchemaVersion string          `json:"schemaversion,omitempty"`
}

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

type AppPublishedEventReceipt struct {
	ReceiptURL string    `json:"receipt_url"`
	ID         string    `json:"id"`
	AcceptedAt time.Time `json:"accepted_at"`
	AccountID  string    `json:"account_id"`
}
type AppPublishEventResponse struct {
	AppID     string                   `json:"app_id"`
	Source    string                   `json:"source"`
	Duplicate bool                     `json:"duplicate"`
	Receipt   AppPublishedEventReceipt `json:"receipt"`
}

func (c *Client) PublishAppEvent(ctx context.Context, slug string, req AppPublishEventRequest) (AppPublishEventResponse, error) {
	var out AppPublishEventResponse
	if err := req.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/events:publish", req, &out)
	return out, err
}
