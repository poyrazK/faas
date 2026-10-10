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
	ExpectedAcceptedAt *time.Time
	Key                string
	After              string
	Limit              int
}

func (q *AppEventPublishStatusQuery) Normalize() error {
	if err := (AppEventAcceptanceGuard{ExpectedAcceptedAt: q.ExpectedAcceptedAt}).Validate(); err != nil {
		return err
	}
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

func (c *Client) GetAppEventPublishStatus(ctx context.Context, slug string, query AppEventPublishStatusQuery) (AppEventPublishStatusResponse, error) {
	var out AppEventPublishStatusResponse
	if err := query.Normalize(); err != nil {
		return out, err
	}
	values := url.Values{"key": {query.Key}, "limit": {strconv.Itoa(query.Limit)}}
	if query.ExpectedAcceptedAt != nil {
		values.Set("expected_accepted_at", query.ExpectedAcceptedAt.UTC().Format(time.RFC3339Nano))
	}
	if query.After != "" {
		values.Set("after", query.After)
	}
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/events/publish-status?"+values.Encode(), nil, &out)
	return out, err
}
