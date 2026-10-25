package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// EventDeadlineExplanations covers only the selected triage page. ReadComplete
// means every row received valid scoped evidence, including unavailable evidence.
// It does not establish handler completion, recovery, or complete backlog coverage.
type EventDeadlineExplanations struct {
	Coverage     string                     `json:"coverage"`
	Attempted    int                        `json:"attempted"`
	ReadComplete bool                       `json:"read_complete"`
	Items        []EventDeadlineExplanation `json:"items"`
}

// EventDeadlineExplanation keeps the exact triage acceptance guard even when a
// later lookup fails, loses retention, or observes a replaced acceptance.
type EventDeadlineExplanation struct {
	Query        EventRecipientBlockerQuery       `json:"query"`
	Status       string                           `json:"status"`
	Reason       string                           `json:"reason,omitempty"`
	ObservedAt   *time.Time                       `json:"observed_at,omitempty"`
	RoutingState string                           `json:"routing_state,omitempty"`
	Routing      *EventDeadlineRoutingExplanation `json:"routing,omitempty"`
}

// EventDeadlineRoutingExplanation projects observed blockers without event data,
// work keys, response URLs, moving ages or unrelated recipient fields.
type EventDeadlineRoutingExplanation struct {
	WaitingReason   string                            `json:"waiting_reason"`
	TargetAvailable bool                              `json:"target_available"`
	CapacityScope   string                            `json:"capacity_scope,omitempty"`
	NextAttemptAt   *time.Time                        `json:"next_attempt_at,omitempty"`
	LeaseUntil      *time.Time                        `json:"lease_until,omitempty"`
	OrderingBlocker *EventDeadlineOrderingExplanation `json:"ordering_blocker,omitempty"`
}

// An ordering blocker may belong to a different subscription in the same lane.
// Its identity is diagnostic metadata, not authorization for a delivery action.
type EventDeadlineOrderingExplanation struct {
	EventSource    string     `json:"event_source"`
	EventID        string     `json:"event_id"`
	SubscriptionID string     `json:"subscription_id"`
	AcceptedAt     time.Time  `json:"accepted_at"`
	State          string     `json:"state"`
	NextAttemptAt  *time.Time `json:"next_attempt_at,omitempty"`
}

func (c *PreviewClient) explainDeadlineInvestigation(ctx context.Context, investigation EventDeadlineInvestigation) (*EventDeadlineExplanations, error) {
	out, request, indices := deadlineExplanationRequest(investigation)
	if len(request.Queries) == 0 {
		return out, ctx.Err()
	}
	batch, err := c.GetEventRecipientBlockers(ctx, request)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for n, index := range indices {
		item := &out.Items[index]
		if len(batch.Items) != len(request.Queries) || !sameRecipientBlockerQuery(batch.Items[n].Query, item.Query) {
			item.Status, item.Reason = "read_error", "invalid_batch_evidence"
		} else {
			out.Items[index] = deadlineExplanationResult(batch.Items[n], item.Query, investigation)
		}
	}
	out.ReadComplete = true
	for _, item := range out.Items {
		if item.Status == "read_error" || item.Status == "not_read" || item.Status == "unsupported_recipient" {
			out.ReadComplete = false
		}
	}
	return out, nil
}

func deadlineExplanationRequest(investigation EventDeadlineInvestigation) (*EventDeadlineExplanations, EventRecipientBlockerBatchRequest, []int) {
	out := &EventDeadlineExplanations{Coverage: "selected_triage_page", ReadComplete: true, Items: make([]EventDeadlineExplanation, len(investigation.Page.Recipients))}
	request := EventRecipientBlockerBatchRequest{Queries: []EventRecipientBlockerQuery{}}
	indices := []int{}
	for i, row := range investigation.Page.Recipients {
		query := EventRecipientBlockerQuery{Source: row.EventSource, ID: row.EventID, SubscriptionID: investigation.Alert.SubscriptionID, ExpectedAcceptedAt: row.AcceptedAt.UTC()}
		item := EventDeadlineExplanation{Query: query, Status: "not_read", Reason: "request_body_budget"}
		if query.Validate() != nil {
			item.Status, item.Reason = "unsupported_recipient", "blocker_query_bounds"
		} else {
			request.Queries = append(request.Queries, query)
			body, err := json.Marshal(request)
			if err != nil || len(body) > EventRecipientBlockerBatchBodyMaxBytes {
				request.Queries = request.Queries[:len(request.Queries)-1]
			} else {
				item.Status, item.Reason = "read_error", "missing_batch_evidence"
				indices = append(indices, i)
			}
		}
		out.Items[i] = item
		out.ReadComplete = false
	}
	out.Attempted = len(request.Queries)
	return out, request, indices
}

func deadlineExplanationResult(item EventRecipientBlockerBatchItem, query EventRecipientBlockerQuery, investigation EventDeadlineInvestigation) EventDeadlineExplanation {
	out := EventDeadlineExplanation{Query: query, Status: "read_error", Reason: "invalid_evidence"}
	if item.Error != "" {
		if item.Result == nil && (item.Error == "read_error" || item.Error == "read_timeout") {
			out.Reason = item.Error
		}
		return out
	}
	r := item.Result
	if r == nil || !sameRecipientBlockerQuery(r.Query, query) || !deadlineInvestigationTime(r.ObservedAt) || r.ObservedAt.Before(investigation.Page.ObservedAt) || !validDeadlineExplanation(*r, investigation.Alert.AppID) {
		return out
	}
	at := r.ObservedAt.UTC()
	out.Status, out.Reason, out.ObservedAt, out.RoutingState = r.Status, r.Reason, &at, r.RoutingState
	if recipient := r.Recipient; recipient != nil {
		out.Routing = &EventDeadlineRoutingExplanation{WaitingReason: recipient.WaitingReason, TargetAvailable: recipient.TargetAvailable, CapacityScope: recipient.CapacityScope, NextAttemptAt: deadlineExplanationTime(recipient.NextAttemptAt), LeaseUntil: deadlineExplanationTime(recipient.LeaseUntil)}
		if b := recipient.OrderingBlocker; b != nil {
			out.Routing.OrderingBlocker = &EventDeadlineOrderingExplanation{EventSource: b.EventSource, EventID: b.EventID, SubscriptionID: b.SubscriptionID, AcceptedAt: b.AcceptedAt.UTC(), State: b.State, NextAttemptAt: deadlineExplanationTime(b.NextAttemptAt)}
		}
	}
	return out
}

func validDeadlineExplanation(r EventRecipientBlockerResponse, appID string) bool {
	switch r.Status {
	case "acceptance_replaced":
		return r.Recipient == nil && r.RoutingState == "" && r.Reason == "acceptance_replaced"
	case "unavailable":
		if r.Recipient != nil {
			return false
		}
		switch r.Reason {
		case "receipt_unavailable", "snapshot_unavailable", "recipient_unavailable":
			return r.RoutingState == ""
		case "routing_evidence_unavailable":
			return deadlineExplanationRoutingState(r.RoutingState)
		}
	case "not_waiting":
		return r.Recipient == nil && r.Reason == "" && (r.RoutingState == "enqueued" || r.RoutingState == "filtered" || r.RoutingState == "failed")
	case "available":
		return r.Reason == "" && validDeadlineExplanationRecipient(r, appID)
	}
	return false
}

func validDeadlineExplanationRecipient(r EventRecipientBlockerResponse, appID string) bool {
	p, q := r.Recipient, r.Query
	if p == nil || p.EventSource != q.Source || p.EventID != q.ID || !p.AcceptedAt.Equal(q.ExpectedAcceptedAt) || !strings.EqualFold(p.SubscriptionID, q.SubscriptionID) || !strings.EqualFold(p.AppID, appID) || p.ConsumerKind != "application" || p.State != r.RoutingState || (p.State != "pending" && p.State != "processing") {
		return false
	}
	_, known := NewEventPendingWaitingReasonCounts()[p.WaitingReason]
	if !known || (EventBacklogFilters{CapacityScope: p.CapacityScope}).Validate() != nil || !validDeadlineExplanationTimes(p.NextAttemptAt, p.LeaseUntil) {
		return false
	}
	if strings.HasPrefix(p.WaitingReason, "capacity_") && p.WaitingReason != "capacity_"+p.CapacityScope {
		return false
	}
	if (p.State == "processing") != (p.WaitingReason == "routing_in_progress") ||
		(p.WaitingReason == "retry_backoff" && (p.NextAttemptAt == nil || !p.NextAttemptAt.After(r.ObservedAt))) ||
		(p.WaitingReason == "receipt_processing" && (p.LeaseUntil == nil || !p.LeaseUntil.After(r.ObservedAt))) {
		return false
	}
	b := p.OrderingBlocker
	if b == nil {
		return p.WaitingReason != "ordering_blocked"
	}
	if b.EventSource == q.Source && b.EventID == q.ID && strings.EqualFold(b.SubscriptionID, q.SubscriptionID) && b.AcceptedAt.Equal(q.ExpectedAcceptedAt) {
		return false
	}
	// Lane ordering spans subscriptions; never substitute the selected subscription.
	return b.EventSource != "" && b.EventID != "" && b.SubscriptionID != "" && deadlineInvestigationTime(b.AcceptedAt) && !b.AcceptedAt.After(r.ObservedAt) && (b.State == "pending" || b.State == "processing") && validDeadlineExplanationTimes(b.NextAttemptAt)
}

func deadlineExplanationRoutingState(state string) bool {
	return state == "pending" || state == "processing" || state == "enqueued" || state == "filtered" || state == "failed"
}

func validDeadlineExplanationTimes(times ...*time.Time) bool {
	for _, at := range times {
		if at != nil && !deadlineInvestigationTime(*at) {
			return false
		}
	}
	return true
}

func deadlineExplanationTime(at *time.Time) *time.Time {
	if at == nil {
		return nil
	}
	cloned := at.UTC()
	return &cloned
}
