package api

import (
	"sort"
	"strings"
	"time"
)

// CommitRecoveryDiagnosticConsumer summarizes only selected, validated recipient samples.
type CommitRecoveryDiagnosticConsumer struct {
	SubscriptionID      string                           `json:"subscription_id"`
	TargetCount         int                              `json:"target_count"`
	Freshness           map[string]int                   `json:"freshness"`
	Statuses            map[string]int                   `json:"statuses"`
	FreshWaitingReasons map[string]int                   `json:"fresh_waiting_reasons"`
	StaleWaitingReasons map[string]int                   `json:"stale_waiting_reasons"`
	Compared            int                              `json:"compared"`
	Changed             int                              `json:"changed"`
	OldestWaiting       *CommitRecoveryDiagnosticWaiting `json:"oldest_waiting,omitempty"`
}

// CommitRecoveryDiagnosticWaiting identifies the oldest sampled waiting acceptance.
// AcceptedAt is acceptance time, never the time a blocker started.
type CommitRecoveryDiagnosticWaiting struct {
	Query         EventRecipientBlockerQuery `json:"query"`
	ObservedAt    time.Time                  `json:"observed_at"`
	Freshness     string                     `json:"freshness"`
	WaitingReason string                     `json:"waiting_reason"`
}

func commitRecoveryDiagnosticConsumers(out CommitRecoveryStatusResult) []CommitRecoveryDiagnosticConsumer {
	progress := out.DiagnosticsProgress
	if progress == nil {
		return nil
	}
	groups := map[string]*CommitRecoveryDiagnosticConsumer{}
	for _, row := range out.Items {
		if !diagnosticTarget(row) {
			continue
		}
		id := strings.ToLower(row.Attempt.SubscriptionID)
		group := groups[id]
		if group == nil {
			group = &CommitRecoveryDiagnosticConsumer{SubscriptionID: id, Freshness: map[string]int{"fresh": 0, "stale": 0, "unread": 0}, Statuses: map[string]int{}, FreshWaitingReasons: map[string]int{}, StaleWaitingReasons: map[string]int{}}
			groups[id] = group
		}
		group.TargetCount++
		freshness := diagnosticFreshness(row, progress.FreshnessCheckedAt, time.Duration(progress.MaxDiagnosticAgeMS)*time.Millisecond)
		group.Freshness[freshness]++
		if !validCommitRecoveryDiagnostic(row.Diagnostics, row) {
			continue
		}
		d := row.Diagnostics
		group.Statuses[d.Status]++
		if !d.Retained && d.Comparison == "compared" {
			group.Compared++
			if len(d.Changes) > 0 {
				group.Changed++
			}
		}
		if d.Status != "available" {
			continue
		}
		reasons := group.FreshWaitingReasons
		if freshness == "stale" {
			reasons = group.StaleWaitingReasons
		}
		reasons[d.Recipient.WaitingReason]++
		publication := row.Selection.Publication
		if group.OldestWaiting == nil || publication.AcceptedAt.Before(group.OldestWaiting.Query.ExpectedAcceptedAt) {
			group.OldestWaiting = &CommitRecoveryDiagnosticWaiting{Query: EventRecipientBlockerQuery{Source: publication.Source, ID: publication.ID, SubscriptionID: id, ExpectedAcceptedAt: publication.AcceptedAt}, ObservedAt: *d.ObservedAt, Freshness: freshness, WaitingReason: d.Recipient.WaitingReason}
		}
	}
	ids := make([]string, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]CommitRecoveryDiagnosticConsumer, 0, len(ids))
	for _, id := range ids {
		result = append(result, *groups[id])
	}
	return result
}
