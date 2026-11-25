package stripeinvoice

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// FindInvoiceSentAt looks for Stripe's delivery event after issuance. Some
// events have no request key, but when Stripe supplies one it must match.
func (c *Client) FindInvoiceSentAt(ctx context.Context, key, invoiceID, sendKey string, livemode bool, issuedAt, until time.Time) (*time.Time, error) {
	q := url.Values{
		"type":         {"invoice.sent"},
		"created[gte]": {strconv.FormatInt(issuedAt.Unix(), 10)},
		"created[lte]": {strconv.FormatInt(until.Unix(), 10)},
		"limit":        {"10"},
	}
	for range 10 {
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
			return nil, err
		}
		for _, event := range result.Data {
			if event.Type == "invoice.sent" && event.Data.Object.ID == invoiceID &&
				event.Livemode != nil && *event.Livemode == livemode &&
				event.Created >= issuedAt.Unix() && event.Created <= until.Unix() &&
				(event.Request.IdempotencyKey == "" || event.Request.IdempotencyKey == sendKey) {
				at := time.Unix(event.Created, 0).UTC()
				return &at, nil
			}
		}
		if !result.HasMore {
			return nil, nil
		}
		if len(result.Data) == 0 {
			break
		}
		after := result.Data[len(result.Data)-1].ID
		if after == "" || after == q.Get("starting_after") {
			break
		}
		q.Set("starting_after", after)
	}
	return nil, errors.New("Stripe invoice delivery search did not finish")
}
