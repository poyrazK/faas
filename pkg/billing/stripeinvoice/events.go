package stripeinvoice

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// FindInvoiceSentAt checks a bounded run of pages for Stripe's delivery event.
// A nonempty cursor resumes after the last completed page, including when
// a later request fails. A match or an exhausted list clears the cursor.
// When Stripe supplies a request key, it must match the reviewed send.
func (c *Client) FindInvoiceSentAt(ctx context.Context, key, invoiceID, sendKey string, livemode bool,
	issuedAt, until time.Time, after string) (sentAt *time.Time, nextCursor string, err error) {
	q := url.Values{
		"type":         {"invoice.sent"},
		"created[gte]": {strconv.FormatInt(issuedAt.Unix(), 10)},
		"created[lte]": {strconv.FormatInt(until.Unix(), 10)},
		"limit":        {"10"},
	}
	for range 10 {
		if after != "" {
			q.Set("starting_after", after)
		}
		var result struct {
			Data []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Created  int64  `json:"created"`
				Livemode *bool  `json:"livemode"`
				Request  struct {
					IdempotencyKey string `json:"idempotency_key"`
				} `json:"request"`
				Data struct {
					Object struct {
						ID string `json:"id"`
					} `json:"object"`
				} `json:"data"`
			} `json:"data"`
			HasMore bool `json:"has_more"`
		}
		if err := c.do(ctx, key, http.MethodGet, "/v1/events?"+q.Encode(), "", nil, &result); err != nil {
			var stripeErr *Error
			if after != "" && errors.As(err, &stripeErr) && stripeErr.Status == http.StatusBadRequest &&
				(stripeErr.Param == "starting_after" || stripeErr.Code == "resource_missing") {
				// A saved event can expire or belong to an old connection.
				// Restart from the newest events on the next check.
				return nil, "", err
			}
			return nil, after, err
		}
		for _, event := range result.Data {
			if event.Type == "invoice.sent" && event.Data.Object.ID == invoiceID &&
				event.Livemode != nil && *event.Livemode == livemode &&
				event.Created >= issuedAt.Unix() && event.Created <= until.Unix() &&
				(event.Request.IdempotencyKey == "" || event.Request.IdempotencyKey == sendKey) {
				at := time.Unix(event.Created, 0).UTC()
				return &at, "", nil
			}
		}
		if !result.HasMore {
			return nil, "", nil
		}
		var cursor string
		if len(result.Data) > 0 {
			cursor = result.Data[len(result.Data)-1].ID
		}
		if cursor == "" || cursor == after {
			return nil, after, errors.New("stripe invoice delivery search returned an invalid cursor")
		}
		after = cursor
	}
	return nil, after, nil
}
