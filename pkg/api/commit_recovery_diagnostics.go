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
	Comparison          string                               `json:"comparison,omitempty"`
	PreviousObservation *CommitRecoveryDiagnosticObservation `json:"previous_observation,omitempty"`
	Changes             []CommitRecoveryDiagnosticChange     `json:"changes,omitempty"`
	Retained            bool                                 `json:"retained,omitempty"`
	Status              string                               `json:"status"`
	Reason              string                               `json:"reason,omitempty"`
	RoutingState        string                               `json:"routing_state,omitempty"`
	ObservedAt          *time.Time                           `json:"observed_at,omitempty"`
	Coverage            string                               `json:"coverage,omitempty"`
	Truncated           bool                                 `json:"truncated,omitempty"`
	Recipient           *EventBacklogRecipient               `json:"recipient,omitempty"`
}

// CommitRecoveryDiagnosticProgress describes diagnostic sampling independently of delivery.
type CommitRecoveryDiagnosticProgress struct {
	Consumers          []CommitRecoveryDiagnosticConsumer `json:"consumers"`
	Compared           int                                `json:"compared"`
	Changed            int                                `json:"changed"`
	MaxDiagnosticAgeMS int64                              `json:"max_diagnostic_age_ms"`
	FreshnessCheckedAt time.Time                          `json:"freshness_checked_at"`
	Freshness          map[string]int                     `json:"freshness"`
	FreshComplete      bool                               `json:"fresh_complete"`
	NextIndex          int                                `json:"next_index"`
	RetryNextIndex     int                                `json:"retry_next_index"`
	RefreshNextIndex   int                                `json:"refresh_next_index"`
	TargetCount        int                                `json:"target_count"`
	Remaining          int                                `json:"remaining"`
	Deferred           int                                `json:"deferred"`
	Retained           int                                `json:"retained"`
	Reads              int                                `json:"reads"`
	Counts             map[string]int                     `json:"counts"`
	Complete           bool                               `json:"complete"`
}

// DiagnoseCommitRecoveryStatus rotates exact lookups within the existing read cap.
// Only call with validated status; retained samples keep their original timestamps.
func (c *Client) DiagnoseCommitRecoveryStatus(ctx context.Context, status CommitRecoveryStatusResult) CommitRecoveryStatusResult {
	out, err := c.diagnoseCommitRecoveryStatus(ctx, status, nil, 0)
	if err != nil {
		return status
	}
	return out
}

// ResumeCommitRecoveryDiagnostics binds earlier samples and cursors to the exact
// validated selection and attempts. Saved aggregate counters are never trusted.
func (c *Client) ResumeCommitRecoveryDiagnostics(ctx context.Context, status, previous CommitRecoveryStatusResult) (CommitRecoveryStatusResult, error) {
	return c.diagnoseCommitRecoveryStatus(ctx, status, &previous, 0)
}

// CommitRecoveryDiagnosticsOptions controls diagnostic age independently of settlement.
type CommitRecoveryDiagnosticsOptions struct{ MaxDiagnosticAge time.Duration }

// DiagnoseCommitRecoveryStatusWithOptions recomputes saved freshness under the current policy.
func (c *Client) DiagnoseCommitRecoveryStatusWithOptions(ctx context.Context, status CommitRecoveryStatusResult, previous *CommitRecoveryStatusResult, options CommitRecoveryDiagnosticsOptions) (CommitRecoveryStatusResult, error) {
	return c.diagnoseCommitRecoveryStatus(ctx, status, previous, options.MaxDiagnosticAge)
}
func validateDiagnosticAge(age time.Duration) error {
	if age < 0 || age > CommitRecoveryDiagnosticAgeMax || (age > 0 && age < CommitRecoveryDiagnosticAgeMin) {
		return ErrValidation("maximum diagnostic age must be zero or 100ms..24h")
	}
	return nil
}
func (c *Client) diagnoseCommitRecoveryStatus(ctx context.Context, status CommitRecoveryStatusResult, previous *CommitRecoveryStatusResult, maxAge time.Duration, strictReads ...bool) (CommitRecoveryStatusResult, error) {
	if err := validateDiagnosticAge(maxAge); err != nil {
		return status, err
	}
	raw, err := json.Marshal(struct {
		Current  CommitRecoveryStatusResult
		Previous *CommitRecoveryStatusResult
	}{status, previous})
	if err != nil {
		return status, err
	}
	var saved struct {
		Current  CommitRecoveryStatusResult
		Previous *CommitRecoveryStatusResult
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		return status, err
	}
	out := saved.Current
	prior := saved.Previous
	if prior == nil {
		prior = &saved.Current
	}
	if !sameDiagnosticSelection(out, *prior) {
		return status, ErrValidation("saved diagnostic selection mismatch")
	}
	progress := &CommitRecoveryDiagnosticProgress{}
	if prior.DiagnosticsProgress != nil {
		progress.NextIndex = diagnosticCursor(prior.DiagnosticsProgress.NextIndex, len(out.Items))
		progress.RetryNextIndex = diagnosticCursor(prior.DiagnosticsProgress.RetryNextIndex, len(out.Items))
		progress.RefreshNextIndex = diagnosticCursor(prior.DiagnosticsProgress.RefreshNextIndex, len(out.Items))
	}
	baselines := make([]*CommitRecoveryDiagnosticObservation, len(out.Items))
	for i := range out.Items {
		row := &out.Items[i]
		old := prior.Items[i].Diagnostics
		baselines[i] = diagnosticObservation(old, *row)
		row.Diagnostics = &CommitRecoveryDiagnostics{Status: "budget_exhausted"}
		if !diagnosticTarget(*row) {
			row.Diagnostics.Status = "unsupported_recipient"
			continue
		}
		if validCommitRecoveryDiagnostic(old, *row) {
			copy := *old
			copy.Retained = true
			copy.Truncated = false
			row.Diagnostics = &copy
			setDiagnosticComparison(row.Diagnostics, old.PreviousObservation, *row)
		} else if old != nil && old.Status == "read_error" {
			row.Diagnostics.Status, row.Diagnostics.Reason = "read_error", old.Reason
		}
		if !diagnosticQueryable(*row) && row.Diagnostics.Status == "budget_exhausted" {
			row.Diagnostics.Status, row.Diagnostics.Reason = "deferred", "receipt_evidence_unavailable"
		}
	}
	unread := diagnosticCandidates(out, progress.NextIndex, 0)
	failed := diagnosticCandidates(out, progress.RetryNextIndex, 1)
	covered := diagnosticCandidates(out, progress.RefreshNextIndex, 2)
	if maxAge > 0 {
		stale, fresh := []int{}, []int{}
		now := time.Now()
		for _, index := range covered {
			if diagnosticFreshness(out.Items[index], now, maxAge) == "stale" {
				stale = append(stale, index)
			} else {
				fresh = append(fresh, index)
			}
		}
		covered = append(stale, fresh...)
	}
	unread = unread[:min(len(unread), CommitRecoveryDiagnosticsReadMax)]
	remaining := CommitRecoveryDiagnosticsReadMax - len(unread)
	reserve := 0
	if len(covered) > 0 && len(failed) > 0 && remaining > CommitRecoveryDiagnosticsRefreshReserve {
		reserve = CommitRecoveryDiagnosticsRefreshReserve
	}
	failed = failed[:min(len(failed), remaining-reserve)]
	remaining -= len(failed)
	covered = covered[:min(len(covered), remaining)]
	queries := []EventRecipientBlockerQuery{}
	indices := []int{}
	groups := []int{}
	for group, selected := range [][]int{unread, failed, covered} {
		for _, index := range selected {
			publication := out.Items[index].Selection.Publication
			queries = append(queries, EventRecipientBlockerQuery{Source: publication.Source, ID: publication.ID, SubscriptionID: strings.ToLower(out.Items[index].Attempt.SubscriptionID), ExpectedAcceptedAt: publication.AcceptedAt.UTC()})
			indices = append(indices, index)
			groups = append(groups, group)
		}
	}
	var transportErr error
	if len(queries) > 0 && ctx.Err() == nil {
		batch, readErr := c.GetEventRecipientBlockers(ctx, EventRecipientBlockerBatchRequest{Queries: queries})
		transportErr = readErr
		for n, index := range indices {
			row := &out.Items[index]
			progress.Reads++
			next := (index + 1) % len(out.Items)
			switch groups[n] {
			case 0:
				progress.NextIndex = next
			case 1:
				progress.RetryNextIndex = next
			case 2:
				progress.RefreshNextIndex = next
			}
			row.Diagnostics = &CommitRecoveryDiagnostics{Status: "read_error"}
			if readErr != nil {
				continue
			}
			if len(batch.Items) != len(queries) || !sameRecipientBlockerQuery(batch.Items[n].Query, queries[n]) {
				row.Diagnostics.Reason = "invalid_evidence"
				continue
			}
			item := batch.Items[n]
			if item.Error != "" {
				continue
			}
			if item.Result == nil {
				row.Diagnostics.Reason = "invalid_evidence"
				continue
			}
			row.Diagnostics = observeCommitRecoveryDiagnostic(*item.Result, queries[n], nil)
			if !validCommitRecoveryDiagnostic(row.Diagnostics, *row) {
				row.Diagnostics = &CommitRecoveryDiagnostics{Status: "read_error", Reason: "invalid_evidence"}
			}
		}
	}
	for i, row := range out.Items {
		if row.Diagnostics.Retained {
			continue
		}
		setDiagnosticComparison(row.Diagnostics, baselines[i], row)
		if row.Diagnostics.Comparison == "compared" {
			progress.Compared++
			if len(row.Diagnostics.Changes) > 0 {
				progress.Changed++
			}
		}
	}
	progress.MaxDiagnosticAgeMS = maxAge.Milliseconds()
	progress.FreshnessCheckedAt = time.Now().UTC()
	progress.Freshness = map[string]int{"fresh": 0, "stale": 0, "unread": 0}
	progress.Counts = map[string]int{"available": 0, "not_waiting": 0, "unavailable": 0, "acceptance_replaced": 0, "read_error": 0, "budget_exhausted": 0, "deferred": 0, "unsupported_recipient": 0}
	for _, row := range out.Items {
		progress.Counts[row.Diagnostics.Status]++
		if !diagnosticTarget(row) {
			continue
		}
		progress.TargetCount++
		progress.Freshness[diagnosticFreshness(row, progress.FreshnessCheckedAt, maxAge)]++
		if !diagnosticQueryable(row) {
			progress.Deferred++
		}
		if !validCommitRecoveryDiagnostic(row.Diagnostics, row) {
			progress.Remaining++
		} else if row.Diagnostics.Retained {
			progress.Retained++
		}
	}
	progress.Complete = progress.Remaining == 0
	progress.FreshComplete = progress.Freshness["fresh"] == progress.TargetCount
	out.DiagnosticsProgress = progress
	progress.Consumers = commitRecoveryDiagnosticConsumers(out)
	if len(strictReads) > 0 && strictReads[0] && transportErr != nil {
		return out, transportErr
	}
	return out, nil
}

func sameDiagnosticSelection(current, previous CommitRecoveryStatusResult) bool {
	if !strings.EqualFold(current.SourceID, previous.SourceID) || !strings.EqualFold(current.SubscriptionID, previous.SubscriptionID) || len(current.Items) != len(previous.Items) {
		return false
	}
	for i, row := range current.Items {
		old := previous.Items[i]
		if row.Selection == nil || old.Selection == nil || !sameRecoveryStatusSelection(*row.Selection, *old.Selection) || !sameRecoveryStatusJSON(row.Attempt, old.Attempt) {
			return false
		}
	}
	return true
}
func diagnosticCursor(cursor, size int) int {
	if cursor < 0 || cursor >= size {
		return 0
	}
	return cursor
}
func diagnosticTarget(row CommitRecoveryStatusItem) bool {
	return row.Selection != nil && row.Status != "unsupported_recipient" && row.Selection.Reviewed.Reason != "unsupported_recipient"
}
func diagnosticQueryable(row CommitRecoveryStatusItem) bool {
	return diagnosticTarget(row) && row.Status != "not_observed" && row.Status != "read_error"
}
func diagnosticCandidates(out CommitRecoveryStatusResult, cursor, group int) []int {
	indices := []int{}
	for n := range len(out.Items) {
		index := (cursor + n) % len(out.Items)
		row := out.Items[index]
		if !diagnosticQueryable(row) {
			continue
		}
		kind := 0
		if validCommitRecoveryDiagnostic(row.Diagnostics, row) {
			kind = 2
		} else if row.Diagnostics.Status == "read_error" {
			kind = 1
		}
		if kind == group {
			indices = append(indices, index)
		}
	}
	return indices
}
func validCommitRecoveryDiagnostic(diagnostic *CommitRecoveryDiagnostics, row CommitRecoveryStatusItem) bool {
	if diagnostic == nil || row.Selection == nil || diagnostic.Coverage != "exact_recipient" || diagnostic.ObservedAt == nil || diagnostic.ObservedAt.IsZero() {
		return false
	}
	switch diagnostic.Status {
	case "unavailable":
		return diagnostic.Recipient == nil && (diagnostic.Reason == "receipt_unavailable" || diagnostic.Reason == "snapshot_unavailable" || diagnostic.Reason == "recipient_unavailable" || diagnostic.Reason == "routing_evidence_unavailable")
	case "acceptance_replaced":
		return diagnostic.Recipient == nil && diagnostic.Reason == "acceptance_replaced"
	case "not_waiting":
		return diagnostic.Recipient == nil && (diagnostic.RoutingState == "enqueued" || diagnostic.RoutingState == "filtered" || diagnostic.RoutingState == "failed")
	case "available":
		recipient := diagnostic.Recipient
		publication := row.Selection.Publication
		return recipient != nil && recipient.EventSource == publication.Source && recipient.EventID == publication.ID && recipient.AcceptedAt.Equal(publication.AcceptedAt) && strings.EqualFold(recipient.SubscriptionID, row.Attempt.SubscriptionID) && recipient.ConsumerKind == "application" && recipient.State == diagnostic.RoutingState && (recipient.State == "pending" || recipient.State == "processing")
	default:
		return false
	}
}

func observeCommitRecoveryDiagnostic(result EventRecipientBlockerResponse, query EventRecipientBlockerQuery, readErr error) *CommitRecoveryDiagnostics {
	diagnostic := &CommitRecoveryDiagnostics{Status: "read_error"}
	if readErr != nil {
		return diagnostic
	}
	if result.Query.Source != query.Source || result.Query.ID != query.ID || result.Query.SubscriptionID != query.SubscriptionID || !result.Query.ExpectedAcceptedAt.Equal(query.ExpectedAcceptedAt) || result.ObservedAt.IsZero() {
		diagnostic.Reason = "invalid_evidence"
		return diagnostic
	}
	diagnostic.ObservedAt = &result.ObservedAt
	diagnostic.Coverage = "exact_recipient"
	diagnostic.Status = result.Status
	diagnostic.Reason = result.Reason
	diagnostic.RoutingState = result.RoutingState
	diagnostic.Recipient = result.Recipient
	return diagnostic
}

func sameRecipientBlockerQuery(a, b EventRecipientBlockerQuery) bool {
	return a.Source == b.Source && a.ID == b.ID && a.SubscriptionID == b.SubscriptionID && a.ExpectedAcceptedAt.Equal(b.ExpectedAcceptedAt)
}

func diagnosticFreshness(row CommitRecoveryStatusItem, now time.Time, maxAge time.Duration) string {
	if !validCommitRecoveryDiagnostic(row.Diagnostics, row) {
		return "unread"
	}
	if maxAge > 0 && (row.Diagnostics.ObservedAt.After(now) || now.Sub(*row.Diagnostics.ObservedAt) > maxAge) {
		return "stale"
	}
	return "fresh"
}

// Recheck after callbacks and at return, because diagnostic age can change while waiting.
func commitRecoveryDiagnosticCoverageComplete(out CommitRecoveryStatusResult, maxAge time.Duration) bool {
	progress := out.DiagnosticsProgress
	if progress == nil {
		return false
	}
	progress.FreshnessCheckedAt = time.Now().UTC()
	progress.MaxDiagnosticAgeMS = maxAge.Milliseconds()
	progress.Freshness = map[string]int{"fresh": 0, "stale": 0, "unread": 0}
	progress.TargetCount = 0
	for _, row := range out.Items {
		if !diagnosticTarget(row) {
			continue
		}
		progress.TargetCount++
		progress.Freshness[diagnosticFreshness(row, progress.FreshnessCheckedAt, maxAge)]++
	}
	progress.Remaining = progress.Freshness["unread"]
	progress.Complete = progress.Remaining == 0
	progress.FreshComplete = progress.Freshness["fresh"] == progress.TargetCount
	progress.Consumers = commitRecoveryDiagnosticConsumers(out)
	return progress.Complete && progress.FreshComplete
}
