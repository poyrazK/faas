package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type AppEventPublicationVerification struct {
	AppID      string                `json:"app_id"`
	Source     string                `json:"source"`
	EventID    string                `json:"event_id"`
	ObservedAt time.Time             `json:"observed_at"`
	Status     string                `json:"status"`
	Reason     string                `json:"reason,omitempty"`
	ReceiptURL string                `json:"receipt_url"`
	Receipt    *PublishEventResponse `json:"receipt,omitempty"`
}

// VerifyAppEventPublication compares retained content without publishing it.
func (c *Client) VerifyAppEventPublication(ctx context.Context, slug string, req AppPublishEventRequest) (AppEventPublicationVerification, error) {
	var out AppEventPublicationVerification
	if err := req.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/events/verify-publication", req, &out)
	return out, err
}
