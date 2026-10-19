package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// EventSubscriptionDeadlineQuery selects effective deadlines, never modeled slots.
type EventSubscriptionDeadlineQuery struct {
	WithinMillis   int64  `json:"WithinMillis,omitempty"`
	WaitingReason  string `json:"WaitingReason,omitempty"`
	DeadlineFrom   *time.Time
	DeadlineBefore *time.Time
	Status         string
	Cursor         string
	Limit          int
}

func (q EventSubscriptionDeadlineQuery) Validate() error {
	if q.WithinMillis != 0 && (!ValidEventSubscriptionDeadlineWindow(q.WithinMillis) || q.Status == "expired") {
		return fmt.Errorf("within_millis requires an upcoming deadline window of 100..86400000 ms")
	}
	if q.WaitingReason != "" {
		if _, ok := NewEventPendingWaitingReasonCounts()[q.WaitingReason]; !ok {
			return fmt.Errorf("unknown waiting_reason")
		}
	}

	if q.Status != "" && q.Status != "all" && q.Status != "expired" && q.Status != "upcoming" {
		return fmt.Errorf("status must be all, expired or upcoming")
	}
	if q.Limit < 0 || q.Limit > EventSubscriptionDeadlinePageMax || len(q.Cursor) > EventSubscriptionDeadlineCursorMaxBytes {
		return fmt.Errorf("invalid deadline page size or cursor")
	}
	for _, at := range []*time.Time{q.DeadlineFrom, q.DeadlineBefore} {
		if at != nil && (at.IsZero() || at.UTC().Year() < 1 || at.UTC().Year() > 9999) {
			return fmt.Errorf("deadline bounds must be valid RFC3339 timestamps")
		}
	}
	if q.DeadlineFrom != nil && q.DeadlineBefore != nil && !q.DeadlineFrom.Before(*q.DeadlineBefore) {
		return fmt.Errorf("deadline_from must precede deadline_before")
	}
	return nil
}

type EventSubscriptionDeadlineRecipient struct {
	EventSource   string     `json:"event_source"`
	EventID       string     `json:"event_id"`
	AcceptedAt    time.Time  `json:"accepted_at"`
	DeadlineAt    time.Time  `json:"deadline_at"`
	Expired       bool       `json:"expired"`
	WaitingReason string     `json:"waiting_reason"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	LeaseUntil    *time.Time `json:"lease_until,omitempty"`
}

type EventSubscriptionDeadlinePage struct {
	Coverage       string                               `json:"coverage"`
	AppID          string                               `json:"app_id"`
	SubscriptionID string                               `json:"subscription_id"`
	ObservedAt     time.Time                            `json:"observed_at"`
	WindowAt       time.Time                            `json:"window_at"`
	ReadOnly       bool                                 `json:"read_only"`
	Recipients     []EventSubscriptionDeadlineRecipient `json:"recipients"`
	HasMore        bool                                 `json:"has_more"`
	NextCursor     string                               `json:"next_cursor,omitempty"`
}

func (c *Client) ListEventSubscriptionDeadlines(ctx context.Context, app, sub string, q EventSubscriptionDeadlineQuery) (EventSubscriptionDeadlinePage, error) {
	var out EventSubscriptionDeadlinePage
	if err := q.Validate(); err != nil {
		return out, ErrValidation(err.Error())
	}
	values := url.Values{}
	if q.WithinMillis != 0 {
		values.Set("within_millis", strconv.FormatInt(q.WithinMillis, 10))
	}
	if q.WaitingReason != "" {
		values.Set("waiting_reason", q.WaitingReason)
	}
	if q.DeadlineFrom != nil {
		values.Set("deadline_from", q.DeadlineFrom.UTC().Format(time.RFC3339Nano))
	}
	if q.DeadlineBefore != nil {
		values.Set("deadline_before", q.DeadlineBefore.UTC().Format(time.RFC3339Nano))
	}
	if q.Status != "" {
		values.Set("status", q.Status)
	}
	if q.Cursor != "" {
		values.Set("cursor", q.Cursor)
	}
	if q.Limit != 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}
	err := c.do(ctx, http.MethodGet, subscriptionDeliveryControlPath(app, sub)+"/deadlines?"+values.Encode(), nil, &out)
	return out, err
}
