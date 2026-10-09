package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type AppEventPublishStatusQuery struct {
	Key   string
	After string
	Limit int
}

func (q *AppEventPublishStatusQuery) Normalize() error {
	if err := ValidateAppEventProducerKey(q.Key); err != nil {
		return err
	}
	if q.Limit == 0 {
		q.Limit = AppEventPublishStatusRecipientsDefault
	}
	if q.Limit < 1 || q.Limit > EventReceiptPageMax || len(q.After) > AppEventPublishStatusCursorMaxBytes {
		return fmt.Errorf("invalid receipt page limit or cursor")
	}
	return nil
}

type AppEventPublishStatusResponse struct {
	AppID      string                `json:"app_id"`
	Source     string                `json:"source"`
	EventID    string                `json:"event_id"`
	ObservedAt time.Time             `json:"observed_at"`
	Status     string                `json:"status"`
	Reason     string                `json:"reason,omitempty"`
	ReceiptURL string                `json:"receipt_url"`
	Receipt    *PublishEventResponse `json:"receipt,omitempty"`
	Evidence   *EventReceiptResponse `json:"evidence,omitempty"`
}

func (c *Client) GetAppEventPublishStatus(ctx context.Context, slug string, query AppEventPublishStatusQuery) (AppEventPublishStatusResponse, error) {
	var out AppEventPublishStatusResponse
	if err := query.Normalize(); err != nil {
		return out, err
	}
	values := url.Values{"key": {query.Key}, "limit": {strconv.Itoa(query.Limit)}}
	if query.After != "" {
		values.Set("after", query.After)
	}
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/events/publish-status?"+values.Encode(), nil, &out)
	return out, err
}
