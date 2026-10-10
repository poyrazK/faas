package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// CommitDeliveryReportItem keeps acceptance separate from sampled delivery evidence.
type CommitDeliveryReportItem struct {
	Acceptance     CommitPublicationHistoryItem `json:"acceptance"`
	EvidenceStatus string                       `json:"evidence_status"`
	Subscribers    []CommitSubscriberWaitResult `json:"subscribers"`
	NextAfter      string                       `json:"next_after,omitempty"`
}

// CommitDeliveryReport is a bounded live read, never an aggregate delivery acknowledgement.
type CommitDeliveryReport struct {
	SourceID       string                     `json:"source_id"`
	SubscriptionID string                     `json:"subscription_id,omitempty"`
	Items          []CommitDeliveryReportItem `json:"items"`
	Counts         map[string]int             `json:"counts"`
	NextBefore     string                     `json:"next_before,omitempty"`
	ReadComplete   bool                       `json:"read_complete"`
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
		out.Items = append(out.Items, CommitDeliveryReportItem{Acceptance: acceptance, EvidenceStatus: "not_read", Subscribers: []CommitSubscriberWaitResult{}})
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
		out.Items = append(out.Items, CommitDeliveryReportItem{Acceptance: acceptance, EvidenceStatus: "not_read", Subscribers: []CommitSubscriberWaitResult{}})
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

func (c *Client) continueCommitDeliveryReport(ctx context.Context, out CommitDeliveryReport, pages int, resume bool) (CommitDeliveryReport, error) {
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
					item.EvidenceStatus = "unavailable"
				case errors.As(err, &apiErr) && apiErr.Problem.Status == 409:
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
			additions := []CommitSubscriberWaitResult{}
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
				additions = append(additions, CommitSubscriberWaitResult{Publication: *publication, SubscriptionID: strings.ToLower(identity.SubscriptionID), Status: status, Recipient: raw})
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
			item.NextAfter = page.NextAfter
			item.EvidenceStatus = "available"
			if page.NextAfter != "" || !page.SnapshotCaptured {
				item.EvidenceStatus = "partial"
			}
			if out.SubscriptionID != "" && len(item.Subscribers) == 0 && item.EvidenceStatus == "available" {
				item.Subscribers = append(item.Subscribers, CommitSubscriberWaitResult{Publication: *publication, SubscriptionID: out.SubscriptionID, Status: "not_targeted"})
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
