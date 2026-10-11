package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

func (c *Client) ListEventRecoveryNotificationRetryHistory(ctx context.Context, id string, options ...EventRecoveryNotificationRetryHistoryQuery) (EventRecoveryNotificationRetryHistory, error) {
	var out EventRecoveryNotificationRetryHistory
	if len(options) > 1 {
		return out, fmt.Errorf("expected at most one retry history query")
	}
	path := "/v1/event-recoveries/" + url.PathEscape(id) + "/notification-retry-decisions"
	if len(options) == 1 && options[0].Status != "" {
		statuses, err := ParseEventRecoveryNotificationRetryHistoryStatus(options[0].Status)
		if err != nil {
			return out, err
		}
		path += "?" + url.Values{"status": {strings.Join(statuses, ",")}}.Encode()
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
func (c *Client) GetEventRecoveryNotificationRetryDecision(ctx context.Context, id, requestID string) (EventRecoveryNotificationRetryDecisionDetail, error) {
	var out EventRecoveryNotificationRetryDecisionDetail
	path := "/v1/event-recoveries/" + url.PathEscape(id) + "/notification-retry-decisions/" + url.PathEscape(requestID)
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// Status is a comma-separated union of request statuses; empty omits the filter.
type EventRecoveryNotificationRetryHistoryQuery struct{ Status string }

func ParseEventRecoveryNotificationRetryHistoryStatus(value string) ([]string, error) {
	statuses := strings.Split(value, ",")
	seen := map[string]bool{}
	for _, status := range statuses {
		switch status {
		case "succeeded", "failed", "pending", "inconclusive":
		default:
			return nil, fmt.Errorf("status must contain only succeeded, failed, pending, or inconclusive")
		}
		if seen[status] {
			return nil, fmt.Errorf("duplicate retry history status")
		}
		seen[status] = true
	}
	sort.Strings(statuses)
	return statuses, nil
}

// Totals always cover all retained requests in the input, before filtering.
func (h *EventRecoveryNotificationRetryHistory) ApplyStatusFilter(statuses []string) {
	totals := EventRecoveryNotificationRetryHistoryTotals{RequestCount: len(h.Decisions)}
	wanted := map[string]bool{}
	for _, status := range statuses {
		wanted[status] = true
	}
	matched := make([]EventRecoveryNotificationRetryDecisionSummary, 0, len(h.Decisions))
	for _, row := range h.Decisions {
		switch row.Status {
		case "succeeded":
			totals.SucceededCount++
		case "failed":
			totals.FailedCount++
		case "pending":
			totals.PendingCount++
		default:
			totals.InconclusiveCount++
		}
		if !row.EvidenceComplete {
			totals.IncompleteEvidenceCount++
		}
		if len(wanted) == 0 || wanted[row.Status] {
			matched = append(matched, row)
		}
	}
	h.Totals, h.MatchedCount, h.Decisions = totals, len(matched), matched
}
