package api

import (
	"strconv"
	"strings"
	"time"
)

// CommitRecoveryDiagnosticObservation is one compact, acceptance-bound historical sample.
// Routing excludes moving ages, counters, leases and server links.
type CommitRecoveryDiagnosticObservation struct {
	Query        EventRecipientBlockerQuery       `json:"query"`
	ObservedAt   time.Time                        `json:"observed_at"`
	Status       string                           `json:"status"`
	RoutingState string                           `json:"routing_state"`
	Routing      *CommitRecoveryDiagnosticRouting `json:"routing,omitempty"`
}
type CommitRecoveryDiagnosticRouting struct {
	WaitingReason   string                            `json:"waiting_reason"`
	CapacityScope   string                            `json:"capacity_scope,omitempty"`
	TargetAvailable *bool                             `json:"target_available"`
	OrderingBlocker *CommitRecoveryDiagnosticOrdering `json:"ordering_blocker,omitempty"`
}
type CommitRecoveryDiagnosticOrdering struct {
	EventSource    string    `json:"event_source"`
	EventID        string    `json:"event_id"`
	SubscriptionID string    `json:"subscription_id"`
	AcceptedAt     time.Time `json:"accepted_at"`
	State          string    `json:"state"`
}

// CommitRecoveryDiagnosticChange describes a field change between two observed snapshots.
type CommitRecoveryDiagnosticChange struct {
	Field    string `json:"field"`
	Previous string `json:"previous"`
	Current  string `json:"current"`
}

func diagnosticObservation(diagnostic *CommitRecoveryDiagnostics, row CommitRecoveryStatusItem) *CommitRecoveryDiagnosticObservation {
	if !validCommitRecoveryDiagnostic(diagnostic, row) || (diagnostic.Status != "available" && diagnostic.Status != "not_waiting") {
		return nil
	}
	publication := row.Selection.Publication
	out := &CommitRecoveryDiagnosticObservation{Query: EventRecipientBlockerQuery{Source: publication.Source, ID: publication.ID, SubscriptionID: strings.ToLower(row.Attempt.SubscriptionID), ExpectedAcceptedAt: publication.AcceptedAt}, ObservedAt: *diagnostic.ObservedAt, Status: diagnostic.Status, RoutingState: diagnostic.RoutingState}
	if recipient := diagnostic.Recipient; recipient != nil {
		out.Routing = &CommitRecoveryDiagnosticRouting{WaitingReason: recipient.WaitingReason, CapacityScope: recipient.CapacityScope, TargetAvailable: &recipient.TargetAvailable}
		if blocker := recipient.OrderingBlocker; blocker != nil {
			out.Routing.OrderingBlocker = &CommitRecoveryDiagnosticOrdering{EventSource: blocker.EventSource, EventID: blocker.EventID, SubscriptionID: blocker.SubscriptionID, AcceptedAt: blocker.AcceptedAt, State: blocker.State}
		}
	}
	if !validDiagnosticObservation(out, out.Query) {
		return nil
	}
	return out
}

func validDiagnosticObservation(observation *CommitRecoveryDiagnosticObservation, query EventRecipientBlockerQuery) bool {
	if observation == nil || !sameRecipientBlockerQuery(observation.Query, query) || observation.Query.Validate() != nil || observation.ObservedAt.IsZero() || (AppEventAcceptanceGuard{ExpectedAcceptedAt: &observation.ObservedAt}).Validate() != nil {
		return false
	}
	if observation.Status == "not_waiting" {
		return observation.Routing == nil && (observation.RoutingState == "enqueued" || observation.RoutingState == "filtered" || observation.RoutingState == "failed")
	}
	routing := observation.Routing
	if observation.Status != "available" || routing == nil || (observation.RoutingState != "pending" && observation.RoutingState != "processing") || routing.TargetAvailable == nil || routing.WaitingReason == "" || (EventBacklogFilters{WaitingReason: routing.WaitingReason, CapacityScope: routing.CapacityScope}).Validate() != nil {
		return false
	}
	blocker := routing.OrderingBlocker
	if blocker == nil {
		return true
	}
	return (EventRecipientBlockerQuery{Source: blocker.EventSource, ID: blocker.EventID, SubscriptionID: blocker.SubscriptionID, ExpectedAcceptedAt: blocker.AcceptedAt}).Validate() == nil && strings.EqualFold(blocker.SubscriptionID, query.SubscriptionID) && (blocker.State == "pending" || blocker.State == "processing")
}

func setDiagnosticComparison(diagnostic *CommitRecoveryDiagnostics, previous *CommitRecoveryDiagnosticObservation, row CommitRecoveryStatusItem) {
	diagnostic.PreviousObservation, diagnostic.Changes = nil, nil
	current := diagnosticObservation(diagnostic, row)
	diagnostic.Comparison = "evidence_unavailable"
	if current == nil {
		return
	}
	diagnostic.Comparison = "baseline_unavailable"
	if !validDiagnosticObservation(previous, current.Query) {
		return
	}
	diagnostic.Comparison = "non_monotonic_timestamp"
	if !current.ObservedAt.After(previous.ObservedAt) {
		return
	}
	diagnostic.Comparison, diagnostic.PreviousObservation = "compared", previous
	before, after := diagnosticRoutingFields(previous), diagnosticRoutingFields(current)
	fields := []string{"status", "routing_state"}
	if previous.Status == "available" && current.Status == "available" {
		fields = append(fields, "waiting_reason", "capacity_scope", "target_available", "ordering_source", "ordering_id", "ordering_subscription_id", "ordering_accepted_at", "ordering_state")
	}
	for _, field := range fields {
		if before[field] != after[field] {
			diagnostic.Changes = append(diagnostic.Changes, CommitRecoveryDiagnosticChange{Field: field, Previous: before[field], Current: after[field]})
		}
	}
}

func diagnosticRoutingFields(observation *CommitRecoveryDiagnosticObservation) map[string]string {
	out := map[string]string{"status": observation.Status, "routing_state": observation.RoutingState}
	if routing := observation.Routing; routing != nil {
		out["waiting_reason"], out["capacity_scope"], out["target_available"] = routing.WaitingReason, routing.CapacityScope, strconv.FormatBool(*routing.TargetAvailable)
		if blocker := routing.OrderingBlocker; blocker != nil {
			out["ordering_source"], out["ordering_id"], out["ordering_subscription_id"] = blocker.EventSource, blocker.EventID, strings.ToLower(blocker.SubscriptionID)
			out["ordering_accepted_at"], out["ordering_state"] = blocker.AcceptedAt.UTC().Format(time.RFC3339Nano), blocker.State
		}
	}
	return out
}
