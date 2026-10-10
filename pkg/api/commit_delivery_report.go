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
	for i := range out.Items {
		item := &out.Items[i]
		publication := item.Acceptance.Publication
		if publication == nil {
			item.EvidenceStatus = "read_error"
			return out, fmt.Errorf("publication history lacks publication identity")
		}
		page, err := c.GetCommitPublicationReceipt(ctx, *publication, "", EventReceiptPageMax)
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
			continue
		}
		if page.EventSource != publication.Source || page.EventID != publication.ID || !page.AcceptedAt.Equal(publication.AcceptedAt) {
			item.EvidenceStatus = "read_error"
			return out, fmt.Errorf("publication evidence identity mismatch")
		}
		item.EvidenceStatus = "available"
		item.NextAfter = page.NextAfter
		if page.NextAfter != "" || !page.SnapshotCaptured {
			item.EvidenceStatus = "partial"
		}
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
			item.Subscribers = append(item.Subscribers, CommitSubscriberWaitResult{Publication: *publication, SubscriptionID: identity.SubscriptionID, Status: status, Recipient: raw})
			out.Counts[status]++
		}
		if out.SubscriptionID != "" && len(item.Subscribers) == 0 && item.EvidenceStatus == "available" {
			item.Subscribers = append(item.Subscribers, CommitSubscriberWaitResult{Publication: *publication, SubscriptionID: out.SubscriptionID, Status: "not_targeted"})
			out.Counts["not_targeted"]++
		}
	}
	out.ReadComplete = true
	return out, nil
}
