package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// EventDeadlineInvestigationOptions bounds one live triage page. Keep the same
// alert and limit when continuing with the returned Page.NextCursor.
type EventDeadlineInvestigationOptions struct {
	Limit  int
	Cursor string
	// Explain reads one acceptance-guarded blocker batch; requires a page limit <=50.
	Explain bool
}

// EventDeadlineAlertReference correlates a triage read with a supplied Go alert.
// AlertAt is notification time, not the original count snapshot's observation.
type EventDeadlineAlertReference struct {
	Metric         string    `json:"metric"`
	AppID          string    `json:"app_id"`
	SubscriptionID string    `json:"subscription_id"`
	IncidentID     string    `json:"incident_id"`
	Sequence       uint64    `json:"sequence"`
	Transition     string    `json:"transition"`
	AlertAt        time.Time `json:"alert_at"`
}

// EventDeadlineInvestigation contains current retained pending identities, not
// historical incident membership or evidence that any delivery recovered.
type EventDeadlineInvestigation struct {
	Alert        EventDeadlineAlertReference    `json:"alert"`
	Membership   string                         `json:"membership"`
	ReadOnly     bool                           `json:"read_only"`
	Query        EventSubscriptionDeadlineQuery `json:"query"`
	Page         EventSubscriptionDeadlinePage  `json:"page"`
	Explanations *EventDeadlineExplanations     `json:"explanations,omitempty"`
}

// InvestigateEventDeadlineAlert performs one read-only triage request and, with
// Explain, at most one blocker batch within the same read budget. It never
// automatically paginates, fetches payloads, acknowledges alerts or retries work.
func (c *Client) InvestigateEventDeadlineAlert(ctx context.Context, app string, alert EventDeliveryHealthAlert, options EventDeadlineInvestigationOptions) (EventDeadlineInvestigation, error) {
	var out EventDeadlineInvestigation
	if strings.TrimSpace(app) == "" || validateDeadlineAlertReference(alert) != nil {
		return out, ErrValidation("deadline investigation requires an app and a valid Go deadline alert reference")
	}
	query, err := deadlineInvestigationQuery(alert.Metric, options)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, EventSubscriptionDeadlineReadTimeout)
	defer cancel()
	page, err := c.ListEventSubscriptionDeadlines(ctx, app, strings.ToLower(alert.SubscriptionID), query)
	if err != nil {
		return out, fmt.Errorf("investigate deadline alert: %w", err)
	}
	if err := validateDeadlineInvestigationPage(page, alert, query); err != nil {
		return out, err
	}
	out.Alert = EventDeadlineAlertReference{Metric: alert.Metric, AppID: strings.ToLower(alert.AppID), SubscriptionID: strings.ToLower(alert.SubscriptionID), IncidentID: alert.IncidentID, Sequence: alert.Sequence, Transition: alert.Transition, AlertAt: alert.At.UTC()}
	out.Membership, out.ReadOnly, out.Query, out.Page = "current_at_triage_observation", true, query, page
	if options.Explain {
		out.Explanations, err = c.explainDeadlineInvestigation(ctx, out)
		if err != nil {
			return EventDeadlineInvestigation{}, fmt.Errorf("explain deadline recipients: %w", err)
		}
	}
	return out, nil
}

func validateDeadlineAlertReference(a EventDeliveryHealthAlert) error {
	for _, id := range []string{a.AppID, a.SubscriptionID} {
		if !commitSubscriberUUID.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
			return ErrValidation("invalid deadline alert scope")
		}
	}
	if !deadlineInvestigationTime(a.At) || a.Sequence == 0 || a.Sequence > EventDeliveryHealthAlertSequenceMax {
		return ErrValidation("invalid deadline alert time or sequence")
	}
	session, _, ok := strings.Cut(a.IncidentID, "-")
	if !ok || len(session) != 32 || session != strings.ToLower(session) {
		return ErrValidation("invalid Go deadline alert incident")
	}
	decoded, err := hex.DecodeString(session)
	if err != nil || len(decoded) != 16 {
		return ErrValidation("invalid Go deadline alert incident")
	}
	opening, ok := deliveryHealthIncidentSequence(a.IncidentID, session)
	if !ok || opening > a.Sequence {
		return ErrValidation("invalid deadline incident sequence")
	}
	switch a.Transition {
	case "opened":
		if opening != a.Sequence {
			return ErrValidation("invalid deadline opening sequence")
		}
	case "resolved", "confirmation_lost":
		if opening >= a.Sequence {
			return ErrValidation("invalid deadline closing sequence")
		}
	default:
		return ErrValidation("unsupported deadline alert transition")
	}
	return nil
}

func deadlineInvestigationQuery(metric string, o EventDeadlineInvestigationOptions) (EventSubscriptionDeadlineQuery, error) {
	q := EventSubscriptionDeadlineQuery{Limit: o.Limit, Cursor: o.Cursor}
	if q.Limit == 0 {
		q.Limit = EventSubscriptionDeadlinePageDefault
	}
	if o.Explain && q.Limit > EventRecipientBlockerBatchMax {
		return q, ErrValidation(fmt.Sprintf("explanations require a page limit of at most %d", EventRecipientBlockerBatchMax))
	}
	if within, expired, reason, ok := deadlineCountWindow(metric); ok {
		q.WaitingReason = reason
		q.Status = "expired"
		if !expired {
			q.Status, q.WithinMillis = "upcoming", within.Milliseconds()
		}
	} else if _, _, ratio := deadlineRatioWindow(metric); ratio {
		q.Status = "expired"
	} else {
		return q, ErrValidation("investigation supports deadline-window count and expired-ratio metrics only")
	}
	if err := q.Validate(); err != nil {
		return q, ErrValidation(err.Error())
	}
	return q, nil
}

func deadlineInvestigationTime(at time.Time) bool {
	return !at.IsZero() && at.UTC().Year() >= 1 && at.UTC().Year() <= 9999
}

func validateDeadlineInvestigationPage(p EventSubscriptionDeadlinePage, a EventDeliveryHealthAlert, q EventSubscriptionDeadlineQuery) error {
	if p.Coverage != "retained_captured_pending_application_deadlines" || !p.ReadOnly || !strings.EqualFold(p.AppID, a.AppID) || !strings.EqualFold(p.SubscriptionID, a.SubscriptionID) || !deadlineInvestigationTime(p.ObservedAt) || !deadlineInvestigationTime(p.WindowAt) || p.WindowAt.After(p.ObservedAt) || (q.Cursor == "" && !p.WindowAt.Equal(p.ObservedAt)) || p.Recipients == nil || len(p.Recipients) > q.Limit || p.HasMore != (p.NextCursor != "") || len(p.NextCursor) > EventSubscriptionDeadlineCursorMaxBytes || (p.HasMore && len(p.Recipients) != q.Limit) || (q.Cursor != "" && p.NextCursor == q.Cursor) {
		return fmt.Errorf("invalid or mismatched deadline investigation page")
	}
	for i, row := range p.Recipients {
		if err := validateDeadlineInvestigationRecipient(row, p, q); err != nil {
			return err
		}
		if i > 0 {
			previous := p.Recipients[i-1]
			if row.DeadlineAt.Before(previous.DeadlineAt) || (row.DeadlineAt.Equal(previous.DeadlineAt) && row.AcceptedAt.Before(previous.AcceptedAt)) {
				return fmt.Errorf("unordered deadline investigation page")
			}
		}
	}
	return nil
}

func validateDeadlineInvestigationRecipient(r EventSubscriptionDeadlineRecipient, p EventSubscriptionDeadlinePage, q EventSubscriptionDeadlineQuery) error {
	_, known := NewEventPendingWaitingReasonCounts()[r.WaitingReason]
	if r.EventSource == "" || r.EventID == "" || !known || !deadlineInvestigationTime(r.AcceptedAt) || r.AcceptedAt.After(p.WindowAt) || !deadlineInvestigationTime(r.DeadlineAt) || !r.DeadlineAt.After(r.AcceptedAt) || r.Expired != !r.DeadlineAt.After(p.ObservedAt) || (q.WaitingReason != "" && r.WaitingReason != q.WaitingReason) {
		return fmt.Errorf("invalid deadline investigation recipient")
	}
	if (q.Status == "expired" && r.DeadlineAt.After(p.WindowAt)) || (q.Status == "upcoming" && (!r.DeadlineAt.After(p.WindowAt) || !r.DeadlineAt.Before(p.WindowAt.Add(time.Duration(q.WithinMillis)*time.Millisecond)))) {
		return fmt.Errorf("deadline investigation recipient outside selected cohort")
	}
	for _, at := range []*time.Time{r.NextAttemptAt, r.LeaseUntil} {
		if at != nil && !deadlineInvestigationTime(*at) {
			return fmt.Errorf("invalid deadline investigation scheduling timestamp")
		}
	}
	return nil
}
