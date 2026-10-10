package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type AppEventPublicationVerification struct {
	Acceptance         string                    `json:"acceptance,omitempty"`
	ExpectedAcceptedAt *time.Time                `json:"expected_accepted_at,omitempty"`
	AppID              string                    `json:"app_id"`
	Source             string                    `json:"source"`
	EventID            string                    `json:"event_id"`
	ObservedAt         time.Time                 `json:"observed_at"`
	Status             string                    `json:"status"`
	Reason             string                    `json:"reason,omitempty"`
	ReceiptURL         string                    `json:"receipt_url"`
	Receipt            *AppPublishedEventReceipt `json:"receipt,omitempty"`
}

// VerifyAppEventPublication compares retained content without publishing it.
func (c *Client) VerifyAppEventPublication(ctx context.Context, slug string, req AppPublishEventRequest, options ...AppEventAcceptanceGuard) (AppEventPublicationVerification, error) {
	var out AppEventPublicationVerification
	if err := req.Validate(); err != nil {
		return out, err
	}
	if len(options) > 1 {
		return out, fmt.Errorf("expected at most one acceptance guard")
	}
	path := "/v1/apps/" + url.PathEscape(slug) + "/events/verify-publication"
	if len(options) == 1 {
		if err := options[0].Validate(); err != nil {
			return out, err
		}
		if options[0].ExpectedAcceptedAt != nil {
			path += "?" + url.Values{"expected_accepted_at": {options[0].ExpectedAcceptedAt.UTC().Format(time.RFC3339Nano)}}.Encode()
		}
	}
	err := c.do(ctx, http.MethodPost, path, req, &out)
	return out, err
}
