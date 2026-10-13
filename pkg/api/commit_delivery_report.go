package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CommitDeliveryReportItem keeps acceptance separate from sampled delivery evidence.
type CommitDeliveryReportItem struct {
	Acceptance     CommitPublicationHistoryItem     `json:"acceptance"`
	EvidenceStatus string                           `json:"evidence_status"`
	Subscribers    []CommitDeliveryReportSubscriber `json:"subscribers"`
	ObservedAt     *time.Time                       `json:"observed_at,omitempty"`
	NextAfter      string                           `json:"next_after,omitempty"`
}

// CommitDeliveryReportSubscriber keeps original execution and retained replay evidence separate.
type CommitDeliveryReportSubscriber struct {
	CommitSubscriberWaitResult
	Recovery *CommitDeliveryReportRecovery `json:"recovery,omitempty"`
}

// CommitDeliveryReportRecovery summarizes only the latest retained generic replay.
// Missing evidence never proves that no replay occurred.
type CommitDeliveryReportRecovery struct {
	Status              string `json:"status"`
	InvocationID        string `json:"invocation_id,omitempty"`
	State               string `json:"state,omitempty"`
	HistoryURL          string `json:"history_url,omitempty"`
	RetainedReplayCount int64  `json:"retained_replay_count,omitempty"`
}

func commitReportRecovery(raw json.RawMessage) (*CommitDeliveryReportRecovery, error) {
	var row struct {
		WorkflowName string `json:"workflow_name"`
		Recovery     *struct {
			RetainedReplayCount int64  `json:"retained_replay_count"`
			HistoryURL          string `json:"history_url"`
			LatestReplay        *struct {
				InvocationID string `json:"invocation_id"`
				State        string `json:"state"`
			} `json:"latest_replay"`
		} `json:"recovery"`
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		return nil, err
	}
	out := &CommitDeliveryReportRecovery{Status: "unavailable"}
	if row.WorkflowName != "" {
		return out, nil
	}
	if r := row.Recovery; r != nil {
		out.HistoryURL = r.HistoryURL
		out.RetainedReplayCount = r.RetainedReplayCount
		if r.LatestReplay != nil {
			out.InvocationID, out.State = r.LatestReplay.InvocationID, r.LatestReplay.State
			switch out.State {
			case "completed":
				out.Status = "recovered"
			case "pending", "dispatching":
				out.Status = "pending"
			case "failed", "dead_letter", "cancelled", "expired", "superseded":
				out.Status = "failed"
			}
		}
	}
	return out, nil
}

func commitRecoveryStatus(row CommitDeliveryReportSubscriber) string {
	if row.Recovery == nil {
		return "unobserved"
	}
	return row.Recovery.Status
}

func recountCommitRecovery(out *CommitDeliveryReport) {
	out.RecoveryCounts = map[string]int{}
	for _, item := range out.Items {
		for _, row := range item.Subscribers {
			if row.Recovery != nil {
				out.RecoveryCounts[row.Recovery.Status]++
			}
		}
	}
}

// CommitDeliveryReport is a bounded live read, never an aggregate delivery acknowledgement.
type CommitDeliveryReport struct {
	SourceID       string                       `json:"source_id"`
	SubscriptionID string                       `json:"subscription_id,omitempty"`
	Items          []CommitDeliveryReportItem   `json:"items"`
	RecoveryCounts map[string]int               `json:"recovery_counts"`
	Counts         map[string]int               `json:"counts"`
	NextBefore     string                       `json:"next_before,omitempty"`
	Changes        []CommitDeliveryReportChange `json:"changes,omitempty"`
	ReadComplete   bool                         `json:"read_complete"`
}

// CommitDeliveryReportChange compares local saved observations with newly read evidence.
type CommitDeliveryReportChange struct {
	EventID            string     `json:"event_id"`
	SubscriptionID     string     `json:"subscription_id,omitempty"`
	Kind               string     `json:"kind"`
	PreviousStatus     string     `json:"previous_status"`
	Status             string     `json:"status"`
	PreviousObservedAt *time.Time `json:"previous_observed_at,omitempty"`
	ObservedAt         *time.Time `json:"observed_at,omitempty"`
}

// RefreshCommitDeliveryReport rereads the saved selection from its first recipient pages.
// Counts contain only fresh observations; a partial refresh never carries old successes forward.
func (c *Client) RefreshCommitDeliveryReport(ctx context.Context, saved CommitDeliveryReport, pages int) (CommitDeliveryReport, error) {
	raw, err := json.Marshal(saved)
	if err != nil {
		return CommitDeliveryReport{}, err
	}
	var out CommitDeliveryReport
	if err := json.Unmarshal(raw, &out); err != nil {
		return CommitDeliveryReport{}, err
	}
	var baseline CommitDeliveryReport
	if err := json.Unmarshal(raw, &baseline); err != nil {
		return CommitDeliveryReport{}, err
	}
	out.ReadComplete = false
	out.Counts = map[string]int{}
	out.RecoveryCounts = map[string]int{}
	validationErr := error(nil)
	if pages < 1 || pages > CommitDeliveryReportPagesMax || len(out.Items) > CommitPublicationHistoryPageMax {
		validationErr = ErrValidation("invalid recipient page budget or saved report")
	} else {
		validationErr = c.validateCommitReportResume(ctx, baseline)
	}
	for i := range out.Items {
		item := &out.Items[i]
		item.Subscribers = []CommitDeliveryReportSubscriber{}
		item.EvidenceStatus = "not_read"
		item.NextAfter = ""
		item.ObservedAt = nil
	}
	out.Changes = nil
	if validationErr != nil {
		return out, validationErr
	}
	out, err = c.continueCommitDeliveryReport(ctx, out, pages, false)
	out.Changes = compareCommitDeliveryReports(baseline, out)
	return out, err
}

func compareCommitDeliveryReports(saved, current CommitDeliveryReport) []CommitDeliveryReportChange {
	changes := []CommitDeliveryReportChange{}
	for i, item := range current.Items {
		previous := saved.Items[i]
		if item.ObservedAt == nil {
			continue
		}
		if item.EvidenceStatus != previous.EvidenceStatus && item.EvidenceStatus != "read_error" && item.EvidenceStatus != "not_read" {
			changes = append(changes, CommitDeliveryReportChange{EventID: item.Acceptance.EventID, Kind: "evidence", PreviousStatus: previous.EvidenceStatus, Status: item.EvidenceStatus, PreviousObservedAt: previous.ObservedAt, ObservedAt: item.ObservedAt})
		}
		before := map[string]string{}
		recoveryBefore := map[string]string{}
		for _, row := range previous.Subscribers {
			before[strings.ToLower(row.SubscriptionID)] = row.Status
			recoveryBefore[strings.ToLower(row.SubscriptionID)] = commitRecoveryStatus(row)
		}
		for _, row := range item.Subscribers {
			old, ok := before[strings.ToLower(row.SubscriptionID)]
			if !ok {
				old = "unobserved"
			}
			if row.Recovery != nil {
				prior, ok := recoveryBefore[strings.ToLower(row.SubscriptionID)]
				if !ok {
					prior = "unobserved"
				}
				if prior != row.Recovery.Status {
					changes = append(changes, CommitDeliveryReportChange{EventID: item.Acceptance.EventID, SubscriptionID: row.SubscriptionID, Kind: "recovery", PreviousStatus: prior, Status: row.Recovery.Status, PreviousObservedAt: previous.ObservedAt, ObservedAt: item.ObservedAt})
				}
			}
			if old != row.Status {
				changes = append(changes, CommitDeliveryReportChange{EventID: item.Acceptance.EventID, SubscriptionID: row.SubscriptionID, Kind: "subscriber", PreviousStatus: old, Status: row.Status, PreviousObservedAt: previous.ObservedAt, ObservedAt: item.ObservedAt})
			}
		}
	}
	return changes
}

// GetCommitDeliveryReport samples one history page and one recipient page per publication.
// NextAfter marks incomplete recipient coverage; errors return the partial report.
func (c *Client) GetCommitDeliveryReport(ctx context.Context, source, before, subscription string, limit int) (CommitDeliveryReport, error) {
	out := CommitDeliveryReport{SourceID: source, SubscriptionID: strings.ToLower(subscription), Items: []CommitDeliveryReportItem{}, Counts: map[string]int{}}
	if subscription != "" && (!commitSubscriberUUID.MatchString(subscription) || subscription == "00000000-0000-0000-0000-000000000000") {
		return out, ErrValidation("invalid subscriber filter")
	}
	history, err := c.ListCommitPublications(ctx, source, before, limit)
	if err != nil {
		return out, err
	}
	out.NextBefore = history.NextBefore
	for _, acceptance := range history.Items {
		out.Items = append(out.Items, CommitDeliveryReportItem{Acceptance: acceptance, EvidenceStatus: "not_read", Subscribers: []CommitDeliveryReportSubscriber{}})
	}
	return c.continueCommitDeliveryReport(ctx, out, 1, false)
}

// GetCommitDeliveryReportWithPages bounds recipient traversal per publication.
func (c *Client) GetCommitDeliveryReportWithPages(ctx context.Context, source, before, subscription string, limit, pages int) (CommitDeliveryReport, error) {
	if pages < 1 || pages > CommitDeliveryReportPagesMax {
		return CommitDeliveryReport{}, ErrValidation("invalid recipient page budget")
	}
	// Select history once; continuation never expands the saved publication selection.
	out := CommitDeliveryReport{SourceID: source, SubscriptionID: strings.ToLower(subscription), Items: []CommitDeliveryReportItem{}, Counts: map[string]int{}}
	if subscription != "" && (!commitSubscriberUUID.MatchString(subscription) || subscription == "00000000-0000-0000-0000-000000000000") {
		return out, ErrValidation("invalid subscriber filter")
	}
	history, err := c.ListCommitPublications(ctx, source, before, limit)
	if err != nil {
		return out, err
	}
	out.NextBefore = history.NextBefore
	for _, acceptance := range history.Items {
		out.Items = append(out.Items, CommitDeliveryReportItem{Acceptance: acceptance, EvidenceStatus: "not_read", Subscribers: []CommitDeliveryReportSubscriber{}})
	}
	return c.continueCommitDeliveryReport(ctx, out, pages, false)
}

// ResumeCommitDeliveryReport copies a saved report and continues unfinished recipients.
// Saved observations are local diagnostics, not signed delivery evidence.
func (c *Client) ResumeCommitDeliveryReport(ctx context.Context, saved CommitDeliveryReport, pages int) (CommitDeliveryReport, error) {
	raw, err := json.Marshal(saved)
	if err != nil {
		return saved, err
	}
	var out CommitDeliveryReport
	if err := json.Unmarshal(raw, &out); err != nil {
		return saved, err
	}
	out.Changes = nil
	return c.continueCommitDeliveryReport(ctx, out, pages, true)
}

func recountCommitReport(out *CommitDeliveryReport) {
	out.Counts = map[string]int{}
	for _, item := range out.Items {
		for _, row := range item.Subscribers {
			out.Counts[row.Status]++
		}
	}
}

func (c *Client) continueCommitDeliveryReport(ctx context.Context, out CommitDeliveryReport, pages int, resume bool) (result CommitDeliveryReport, resultErr error) {
	defer func() { recountCommitRecovery(&result) }()
	out.ReadComplete = false
	recountCommitReport(&out)
	if pages < 1 || pages > CommitDeliveryReportPagesMax || len(out.Items) > CommitPublicationHistoryPageMax {
		return out, ErrValidation("invalid recipient page budget or saved report")
	}
	if resume {
		if err := c.validateCommitReportResume(ctx, out); err != nil {
			return out, err
		}
	}
	for i := range out.Items {
		item := &out.Items[i]
		if resume && (item.EvidenceStatus == "available" || item.EvidenceStatus == "unavailable" || item.EvidenceStatus == "acceptance_replaced") {
			continue
		}
		publication := item.Acceptance.Publication
		if publication == nil {
			item.EvidenceStatus = "read_error"
			return out, fmt.Errorf("publication history lacks publication identity")
		}
		seen := map[string]bool{}
		for n := 0; n < pages; n++ {
			after := item.NextAfter
			if seen[after] {
				item.EvidenceStatus = "read_error"
				return out, fmt.Errorf("repeated publication recipient cursor")
			}
			seen[after] = true
			page, err := c.GetCommitPublicationReceipt(ctx, *publication, after, EventReceiptPageMax)
			if err != nil {
				var apiErr *APIError
				switch {
				case errors.As(err, &apiErr) && apiErr.Problem.Status == 404:
					at := time.Now().UTC()
					item.ObservedAt = &at
					item.EvidenceStatus = "unavailable"
				case errors.As(err, &apiErr) && apiErr.Problem.Status == 409:
					at := time.Now().UTC()
					item.ObservedAt = &at
					item.EvidenceStatus = "acceptance_replaced"
				default:
					item.EvidenceStatus = "read_error"
					return out, err
				}
				break
			}
			if page.EventSource != publication.Source || page.EventID != publication.ID || !page.AcceptedAt.Equal(publication.AcceptedAt) || (page.NextAfter != "" && (page.NextAfter == after || seen[page.NextAfter])) {
				item.EvidenceStatus = "read_error"
				return out, fmt.Errorf("publication identity or pagination mismatch")
			}
			additions := []CommitDeliveryReportSubscriber{}
			for _, recipient := range page.Recipients {
				raw, err := json.Marshal(recipient)
				if err != nil {
					item.EvidenceStatus = "read_error"
					return out, err
				}
				var identity commitWaitRecipient
				if err := json.Unmarshal(raw, &identity); err != nil {
					item.EvidenceStatus = "read_error"
					return out, err
				}
				if identity.Origin != "" && identity.Origin != "acceptance" {
					continue
				}
				if out.SubscriptionID != "" && !strings.EqualFold(identity.SubscriptionID, out.SubscriptionID) {
					continue
				}
				status, _, err := commitSubscriberStatus(raw, identity.SubscriptionID)
				if err != nil {
					item.EvidenceStatus = "read_error"
					return out, err
				}
				recovery, err := commitReportRecovery(raw)
				if err != nil {
					item.EvidenceStatus = "read_error"
					return out, err
				}
				additions = append(additions, CommitDeliveryReportSubscriber{CommitSubscriberWaitResult: CommitSubscriberWaitResult{Publication: *publication, SubscriptionID: strings.ToLower(identity.SubscriptionID), Status: status, Recipient: raw}, Recovery: recovery})
			}
			// Merge only a fully decoded page; retries replace each subscriber's observation.
			for _, row := range additions {
				replaced := false
				for j := range item.Subscribers {
					if strings.EqualFold(item.Subscribers[j].SubscriptionID, row.SubscriptionID) {
						old := item.Subscribers[j].Status
						out.Counts[old]--
						if out.Counts[old] == 0 {
							delete(out.Counts, old)
						}
						item.Subscribers[j] = row
						replaced = true
						break
					}
				}
				if !replaced {
					item.Subscribers = append(item.Subscribers, row)
				}
				out.Counts[row.Status]++
			}
			at := time.Now().UTC()
			item.ObservedAt = &at
			item.NextAfter = page.NextAfter
			item.EvidenceStatus = "available"
			if page.NextAfter != "" || !page.SnapshotCaptured {
				item.EvidenceStatus = "partial"
			}
			if out.SubscriptionID != "" && len(item.Subscribers) == 0 && item.EvidenceStatus == "available" {
				item.Subscribers = append(item.Subscribers, CommitDeliveryReportSubscriber{CommitSubscriberWaitResult: CommitSubscriberWaitResult{Publication: *publication, SubscriptionID: out.SubscriptionID, Status: "not_targeted"}})
				out.Counts["not_targeted"]++
			}
			if item.NextAfter == "" {
				break
			}
		}
	}
	out.ReadComplete = true
	return out, nil
}

func (c *Client) validateCommitReportResume(ctx context.Context, out CommitDeliveryReport) error {
	if !commitSubscriberUUID.MatchString(out.SourceID) || (out.SubscriptionID != "" && (!commitSubscriberUUID.MatchString(out.SubscriptionID) || out.SubscriptionID == "00000000-0000-0000-0000-000000000000")) {
		return ErrValidation("invalid saved report identity")
	}
	source, err := c.GetCommitSource(ctx, out.SourceID)
	if err != nil {
		return err
	}
	if source.Destination != "event" {
		return ErrValidation("saved report requires an event source")
	}
	seen := map[string]bool{}
	for _, item := range out.Items {
		a := item.Acceptance
		p := a.Publication
		if p == nil || !strings.EqualFold(a.SourceID, out.SourceID) || !commitSubscriberUUID.MatchString(a.EventID) || seen[strings.ToLower(a.EventID)] || p.Source != "app."+source.AppID || p.ID != "commit."+strings.ToLower(out.SourceID)+"."+strings.ToLower(a.EventID) || p.AcceptedAt.IsZero() {
			return ErrValidation("saved publication identity mismatch")
		}
		seen[strings.ToLower(a.EventID)] = true
		switch item.EvidenceStatus {
		case "available", "partial", "unavailable", "acceptance_replaced", "not_read", "read_error":
		default:
			return ErrValidation("invalid saved evidence status")
		}
		subs := map[string]bool{}
		for _, row := range item.Subscribers {
			id := strings.ToLower(row.SubscriptionID)
			if !commitSubscriberUUID.MatchString(id) || subs[id] || (out.SubscriptionID != "" && !strings.EqualFold(id, out.SubscriptionID)) {
				return ErrValidation("invalid saved subscriber identity")
			}
			subs[id] = true
		}
	}
	return nil
}
