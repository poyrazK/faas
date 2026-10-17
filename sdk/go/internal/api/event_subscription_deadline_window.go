package api

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// EventSubscriptionDeadlineWindow counts complete, disjoint deadline cohorts.
// The upcoming interval is strictly after Summary.ObservedAt and before WindowEndAt.
type EventSubscriptionDeadlineWindow struct {
	Coverage                   string                          `json:"coverage"`
	Summary                    EventSubscriptionPendingSummary `json:"summary"`
	WithinMillis               int64                           `json:"within_millis"`
	WindowEndAt                time.Time                       `json:"window_end_at"`
	UpcomingRecipients         int64                           `json:"upcoming_recipients"`
	EarliestUpcomingDeadlineAt *time.Time                      `json:"earliest_upcoming_deadline_at,omitempty"`
	UpcomingWaitingReasons     map[string]int64                `json:"upcoming_waiting_reasons"`
	ExpiredWaitingReasons      map[string]int64                `json:"expired_waiting_reasons"`
}

func ValidEventSubscriptionDeadlineWindow(millis int64) bool {
	return millis >= EventSubscriptionDeadlineWindowMinMillis && millis <= EventSubscriptionDeadlineWindowMaxMillis
}
func (c *Client) GetEventSubscriptionDeadlineWindow(ctx context.Context, app, sub string, withinMillis int64) (EventSubscriptionDeadlineWindow, error) {
	var out EventSubscriptionDeadlineWindow
	if !ValidEventSubscriptionDeadlineWindow(withinMillis) {
		return out, ErrValidation("within_millis must be 100..86400000 whole milliseconds")
	}
	err := c.do(ctx, http.MethodGet, subscriptionDeliveryControlPath(app, sub)+"/deadline-window?within_millis="+strconv.FormatInt(withinMillis, 10), nil, &out)
	return out, err
}

// EventSubscriptionDeadlineWindowCountsComplete requires exact known partitions.
// Missing or unknown reason keys, incomplete reads and inconsistent clocks fail closed.
func EventSubscriptionDeadlineWindowCountsComplete(v EventSubscriptionDeadlineWindow) bool {
	s := v.Summary
	if v.Coverage != "all_retained_captured_pending_application_deadline_window" || !s.ReadOnly || !s.Complete || s.ObservedAt.IsZero() || !ValidEventSubscriptionDeadlineWindow(v.WithinMillis) || !v.WindowEndAt.Equal(s.ObservedAt.Add(time.Duration(v.WithinMillis)*time.Millisecond)) || v.WindowEndAt.Year() > 9999 {
		return false
	}
	if s.PendingRecipients < 0 || s.PendingRecipients > EventSubscriptionPendingSummaryCountMax || s.DeadlineRecipients < 0 || s.DeadlineRecipients > s.PendingRecipients || s.AlreadyExpiredCount < 0 || s.AlreadyExpiredCount > s.DeadlineRecipients || v.UpcomingRecipients < 0 || v.UpcomingRecipients > s.DeadlineRecipients-s.AlreadyExpiredCount {
		return false
	}
	if !EventPendingWaitingReasonCountsComplete(v.UpcomingWaitingReasons, v.UpcomingRecipients) || !EventPendingWaitingReasonCountsComplete(v.ExpiredWaitingReasons, s.AlreadyExpiredCount) || (v.UpcomingRecipients == 0) != (v.EarliestUpcomingDeadlineAt == nil) {
		return false
	}
	if v.EarliestUpcomingDeadlineAt != nil && (!v.EarliestUpcomingDeadlineAt.After(s.ObservedAt) || !v.EarliestUpcomingDeadlineAt.Before(v.WindowEndAt)) {
		return false
	}
	if s.EarliestDeadlineAt != nil && s.EarliestDeadlineAt.After(s.ObservedAt) {
		if s.EarliestDeadlineAt.Before(v.WindowEndAt) {
			return v.EarliestUpcomingDeadlineAt != nil && v.EarliestUpcomingDeadlineAt.Equal(*s.EarliestDeadlineAt)
		}
		return v.UpcomingRecipients == 0
	}
	return true
}

const (
	EventSubscriptionDeadlineWindowMinMillis int64 = 100
	EventSubscriptionDeadlineWindowMaxMillis int64 = 86400000
)
