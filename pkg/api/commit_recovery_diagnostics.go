package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// CommitRecoveryDiagnostics is a separate, bounded snapshot of live routing blockers.
// Missing evidence never proves that delivery is ready or terminal.
type CommitRecoveryDiagnostics struct {
	Status       string                 `json:"status"`
	Reason       string                 `json:"reason,omitempty"`
	RoutingState string                 `json:"routing_state,omitempty"`
	ObservedAt   *time.Time             `json:"observed_at,omitempty"`
	Coverage     string                 `json:"coverage,omitempty"`
	Truncated    bool                   `json:"truncated,omitempty"`
	Recipient    *EventBacklogRecipient `json:"recipient,omitempty"`
}

// DiagnoseCommitRecoveryStatus clones status and performs exact-recipient lookups,
// up to CommitRecoveryDiagnosticsReadMax reads. It never changes receipt evidence,
// attribution, settlement, or the saved recovery attempt. Call only with a freshly
// validated status result, not an unvalidated saved file.
func (c *Client) DiagnoseCommitRecoveryStatus(ctx context.Context, status CommitRecoveryStatusResult) CommitRecoveryStatusResult {
	raw, err := json.Marshal(status)
	if err != nil {
		return status
	}
	var out CommitRecoveryStatusResult
	if json.Unmarshal(raw, &out) != nil {
		return status
	}
	type sample struct {
		result EventRecipientBlockerResponse
		err    error
	}
	cache := map[EventRecipientBlockerQuery]sample{}
	for i := range out.Items {
		row := &out.Items[i]
		diagnostic := &CommitRecoveryDiagnostics{Status: "unavailable"}
		row.Diagnostics = diagnostic
		if row.Selection == nil || row.Status == "not_observed" || row.Status == "read_error" || row.Status == "unsupported_recipient" {
			continue
		}
		publication := row.Selection.Publication
		query := EventRecipientBlockerQuery{Source: publication.Source, ID: publication.ID, SubscriptionID: strings.ToLower(row.Attempt.SubscriptionID), ExpectedAcceptedAt: publication.AcceptedAt.UTC()}
		snapshot, exists := cache[query]
		if !exists {
			if len(cache) >= CommitRecoveryDiagnosticsReadMax {
				diagnostic.Status = "budget_exhausted"
				continue
			}
			if ctx.Err() != nil {
				diagnostic.Status = "read_error"
				continue
			}
			result, readErr := c.GetEventRecipientBlocker(ctx, query)
			snapshot = sample{result: result, err: readErr}
			cache[query] = snapshot
		}
		if snapshot.err != nil {
			diagnostic.Status = "read_error"
			continue
		}
		result := snapshot.result
		if result.Query.Source != query.Source || result.Query.ID != query.ID || result.Query.SubscriptionID != query.SubscriptionID || !result.Query.ExpectedAcceptedAt.Equal(query.ExpectedAcceptedAt) || result.ObservedAt.IsZero() {
			continue
		}
		diagnostic.ObservedAt = &result.ObservedAt
		diagnostic.Coverage = "exact_recipient"
		switch result.Status {
		case "unavailable", "acceptance_replaced":
			diagnostic.Status, diagnostic.Reason = result.Status, result.Reason
		case "not_waiting":
			if result.Recipient == nil && (result.RoutingState == "enqueued" || result.RoutingState == "filtered" || result.RoutingState == "failed") {
				diagnostic.Status, diagnostic.RoutingState = result.Status, result.RoutingState
			}
		case "available":
			recipient := result.Recipient
			if recipient != nil && recipient.EventSource == query.Source && recipient.EventID == query.ID && recipient.AcceptedAt.Equal(query.ExpectedAcceptedAt) && strings.EqualFold(recipient.SubscriptionID, query.SubscriptionID) && recipient.ConsumerKind == "application" && recipient.State == result.RoutingState && (recipient.State == "pending" || recipient.State == "processing") {
				diagnostic.Status, diagnostic.RoutingState, diagnostic.Recipient = "available", result.RoutingState, recipient
			}
		}
	}
	return out
}
