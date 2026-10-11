package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// CommitRecoveryDiagnostics is a separate, bounded snapshot of live routing blockers.
// Missing backlog evidence never proves that delivery is ready or terminal.
type CommitRecoveryDiagnostics struct {
	Status     string                 `json:"status"`
	ObservedAt *time.Time             `json:"observed_at,omitempty"`
	Coverage   string                 `json:"coverage,omitempty"`
	Truncated  bool                   `json:"truncated,omitempty"`
	Recipient  *EventBacklogRecipient `json:"recipient,omitempty"`
}

// DiagnoseCommitRecoveryStatus clones status and samples one backlog page per
// subscription, up to CommitRecoveryDiagnosticsReadMax reads. It never changes
// receipt evidence, attribution, settlement, or the saved recovery attempt.
// Call only with a freshly validated status result, not an unvalidated saved file.
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
		page EventBacklogResponse
		err  error
	}
	cache := map[string]sample{}
	for i := range out.Items {
		row := &out.Items[i]
		diagnostic := &CommitRecoveryDiagnostics{Status: "unavailable"}
		row.Diagnostics = diagnostic
		if row.Selection == nil || row.Status == "not_observed" || row.Status == "read_error" {
			continue
		}
		subscription := strings.ToLower(row.Attempt.SubscriptionID)
		snapshot, exists := cache[subscription]
		if !exists {
			if len(cache) >= CommitRecoveryDiagnosticsReadMax {
				diagnostic.Status = "budget_exhausted"
				continue
			}
			if ctx.Err() != nil {
				diagnostic.Status = "read_error"
				continue
			}
			page, readErr := c.GetEventBacklog(ctx, EventBacklogOptions{EventBacklogFilters: EventBacklogFilters{SubscriptionID: subscription}, Limit: EventBacklogPageMax, ConsumerLimit: 1})
			snapshot = sample{page: page, err: readErr}
			cache[subscription] = snapshot
		}
		if snapshot.err != nil {
			diagnostic.Status = "read_error"
			continue
		}
		page := snapshot.page
		diagnostic.ObservedAt = &page.ObservedAt
		diagnostic.Coverage = page.Coverage
		diagnostic.Truncated = page.NextAfter != ""
		diagnostic.Status = "not_found"
		if page.Coverage != EventBacklogCoverage || page.ObservedAt.IsZero() {
			diagnostic.Status = "unavailable"
			continue
		}
		publication := row.Selection.Publication
		for _, recipient := range page.Recipients {
			if recipient.EventSource == publication.Source && recipient.EventID == publication.ID && recipient.AcceptedAt.Equal(publication.AcceptedAt) && strings.EqualFold(recipient.SubscriptionID, subscription) && recipient.ConsumerKind == "application" {
				diagnostic.Status = "available"
				diagnostic.Recipient = &recipient
				break
			}
		}
	}
	return out
}

const CommitRecoveryDiagnosticsReadMax = 50
