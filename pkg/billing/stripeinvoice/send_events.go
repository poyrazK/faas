package stripeinvoice

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// InvoiceSendEventLookback is the Events API's retention for v1 events.
const InvoiceSendEventLookback = 30 * 24 * time.Hour

// ErrInvoiceSendEventSearchLimit reports that a bounded lookup could not
// read all events in the relevant time range.
var ErrInvoiceSendEventSearchLimit = errors.New("stripe invoice send event search limit reached")

type InvoiceSendEvent struct {
	ID        string
	CreatedAt time.Time
}

// FindInvoiceSendEvent looks for an invoice.sent event while bounding the
// number and size of provider reads. An empty ID means no matching event.
func (c *Client) FindInvoiceSendEvent(ctx context.Context, key, invoiceID string, live bool, from, until time.Time) (InvoiceSendEvent, error) {
	return c.findInvoiceEvent(ctx, key, "invoice.sent", invoiceID, live, from, until)
}

// HasInvoiceDeletionEvent verifies that Stripe deleted the draft.
func (c *Client) HasInvoiceDeletionEvent(ctx context.Context, key, invoiceID string, live bool, from, until time.Time) (bool, error) {
	event, err := c.findInvoiceEvent(ctx, key, "invoice.deleted", invoiceID, live, from, until)
	return event.ID != "", err
}

func (c *Client) findInvoiceEvent(ctx context.Context, key, eventType, invoiceID string, live bool, from, until time.Time) (InvoiceSendEvent, error) {
	params := url.Values{
		"type": {eventType}, "limit": {"25"},
		"created[gte]": {strconv.FormatInt(from.Unix(), 10)},
		"created[lte]": {strconv.FormatInt(until.Unix(), 10)},
	}
	for range 4 {
		var page struct {
			HasMore bool `json:"has_more"`
			Data    []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Livemode *bool  `json:"livemode"`
				Created  int64  `json:"created"`
				Data     struct {
					Object struct {
						ID     string `json:"id"`
						Object string `json:"object"`
					} `json:"object"`
				} `json:"data"`
			} `json:"data"`
		}
		if err := c.do(ctx, key, http.MethodGet, "/v1/events?"+params.Encode(), "", nil, &page); err != nil {
			return InvoiceSendEvent{}, err
		}
		for _, event := range page.Data {
			if event.ID != "" && event.Type == eventType && event.Livemode != nil && *event.Livemode == live &&
				event.Created >= from.Unix() && event.Created <= until.Unix() &&
				event.Data.Object.Object == "invoice" && event.Data.Object.ID == invoiceID {
				return InvoiceSendEvent{ID: event.ID, CreatedAt: time.Unix(event.Created, 0).UTC()}, nil
			}
		}
		if !page.HasMore {
			return InvoiceSendEvent{}, nil
		}
		if len(page.Data) == 0 {
			break
		}
		cursor := page.Data[len(page.Data)-1].ID
		if cursor == "" || cursor == params.Get("starting_after") {
			break
		}
		params.Set("starting_after", cursor)
	}
	return InvoiceSendEvent{}, ErrInvoiceSendEventSearchLimit
}
