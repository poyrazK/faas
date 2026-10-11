package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// CommitRecoveryStatusResult samples delivery evidence without submitting recovery.
type CommitRecoveryStatusResult struct {
	SourceID string                     `json:"source_id"`
	Items    []CommitRecoveryStatusItem `json:"items"`
	Counts   map[string]int             `json:"counts"`
	Complete bool                       `json:"complete"`
}
type CommitRecoveryStatusItem struct {
	Attempt            CommitRecoveryApplyItem `json:"attempt"`
	Status             string                  `json:"status"`
	Reason             string                  `json:"reason,omitempty"`
	Attribution        string                  `json:"attribution"`
	ObservedAt         *time.Time              `json:"observed_at,omitempty"`
	ExpectedGeneration *int64                  `json:"expected_generation,omitempty"`
	Generation         *int64                  `json:"generation,omitempty"`
	GenerationRelation string                  `json:"generation_relation,omitempty"`
	Recipient          json.RawMessage         `json:"recipient,omitempty"`
}

// GetCommitRecoveryStatus keeps unknown attempt outcomes unknown, even if delivery advanced.
// It reads only guarded receipt evidence and never follows saved action URLs.
func (c *Client) GetCommitRecoveryStatus(ctx context.Context, reviewed CommitRecoveryPlan, applied CommitRecoveryApplyResult, limit, pages int) (CommitRecoveryStatusResult, error) {
	out := CommitRecoveryStatusResult{SourceID: reviewed.Report.SourceID, Items: []CommitRecoveryStatusItem{}, Counts: map[string]int{}}
	raw, err := json.Marshal(struct {
		Plan    CommitRecoveryPlan
		Applied CommitRecoveryApplyResult
	}{reviewed, applied})
	if err != nil {
		return out, err
	}
	var saved struct {
		Plan    CommitRecoveryPlan
		Applied CommitRecoveryApplyResult
	}
	if err = json.Unmarshal(raw, &saved); err != nil {
		return out, err
	}
	plan, result := saved.Plan, saved.Applied
	maxItems := CommitPublicationHistoryPageMax * AppEventPublishStatusRecipientsMax * CommitDeliveryReportPagesMax
	if limit < 1 || limit > CommitRecoveryStatusMax || pages < 1 || pages > CommitDeliveryReportPagesMax || len(plan.Report.Items) > CommitPublicationHistoryPageMax || len(plan.Items) > maxItems || len(result.Items) > maxItems || !strings.EqualFold(result.SourceID, plan.Report.SourceID) {
		return out, ErrValidation("invalid recovery status budgets or source")
	}
	publications := map[string]CommitDeliveryReportItem{}
	for _, item := range plan.Report.Items {
		publications[strings.ToLower(item.Acceptance.EventID)] = item
	}
	selection := map[string]CommitRecoveryPlanItem{}
	for _, item := range plan.Items {
		key := strings.ToLower(item.EventID + ":" + item.SubscriptionID)
		_, exists := publications[strings.ToLower(item.EventID)]
		if !exists || !commitSubscriberUUID.MatchString(item.SubscriptionID) || item.SubscriptionID == "00000000-0000-0000-0000-000000000000" || selection[key].EventID != "" || (plan.Report.SubscriptionID != "" && !strings.EqualFold(item.SubscriptionID, plan.Report.SubscriptionID)) || (item.Status != "eligible" && item.Status != "skipped") || (item.Status == "eligible" && item.Action == nil) {
			return out, ErrValidation("invalid recovery status selection")
		}
		selection[key] = item
	}
	seen := map[string]bool{}
	for _, item := range result.Items {
		key := strings.ToLower(item.EventID + ":" + item.SubscriptionID)
		selected, exists := selection[key]
		if !exists || seen[key] {
			return out, ErrValidation("apply result does not match reviewed selection")
		}
		switch item.Status {
		case "queued", "unknown", "rejected", "read_error":
			if selected.Status != "eligible" {
				return out, ErrValidation("attempt requires reviewed eligibility")
			}
		case "skipped", "not_attempted":
		default:
			return out, ErrValidation("invalid saved attempt status")
		}
		seen[key] = true
	}
	if err = c.validateCommitReportResume(ctx, plan.Report); err != nil {
		return out, err
	}
	var firstErr error
	out.Complete = true
	for i, attempt := range result.Items {
		selected := selection[strings.ToLower(attempt.EventID+":"+attempt.SubscriptionID)]
		row := CommitRecoveryStatusItem{Attempt: attempt, Status: "unresolved", Reason: "evidence_unavailable", Attribution: "unproven", ExpectedGeneration: commitRoutingGeneration(selected.Action)}
		if i >= limit || ctx.Err() != nil {
			row.Status = "not_observed"
			row.Reason = "status_budget"
			if ctx.Err() != nil {
				row.Reason = "interrupted"
				if firstErr == nil {
					firstErr = ctx.Err()
				}
			}
			out.Complete = false
		} else {
			original := publications[strings.ToLower(attempt.EventID)]
			original.Subscribers = []CommitDeliveryReportSubscriber{}
			current, readErr := c.RefreshCommitDeliveryReport(ctx, CommitDeliveryReport{SourceID: plan.Report.SourceID, SubscriptionID: strings.ToLower(attempt.SubscriptionID), Items: []CommitDeliveryReportItem{original}}, pages)
			if len(current.Items) > 0 {
				evidence := current.Items[0]
				row.ObservedAt = evidence.ObservedAt
				row.Reason = evidence.EvidenceStatus
				if len(evidence.Subscribers) > 0 {
					classifyCommitRecoveryStatus(&row, evidence.Subscribers[0].Recipient)
				} else if evidence.EvidenceStatus == "available" {
					row.Reason = "recipient_unavailable"
				}
			}
			if readErr != nil {
				row.Status = "read_error"
				row.Reason = "evidence_read_failed"
				out.Complete = false
				if firstErr == nil {
					firstErr = readErr
				}
			}
		}
		out.Items = append(out.Items, row)
		out.Counts[row.Status]++
	}
	return out, firstErr
}

func classifyCommitRecoveryStatus(out *CommitRecoveryStatusItem, raw json.RawMessage) {
	type execution struct {
		ID    string `json:"invocation_id"`
		State string `json:"state"`
	}
	var r struct {
		Workflow             string          `json:"workflow_name"`
		ExecutionUnavailable string          `json:"execution_unavailable"`
		Cancellation         json.RawMessage `json:"cancellation"`
		Routing              struct {
			Generation *int64 `json:"generation"`
			State      string `json:"state"`
		} `json:"routing"`
		Execution *execution `json:"execution"`
		Recovery  *struct {
			Latest *execution `json:"latest_replay"`
		} `json:"recovery"`
	}
	out.Recipient = raw
	if json.Unmarshal(raw, &r) != nil {
		out.Reason = "evidence_unavailable"
		return
	}
	out.Generation = r.Routing.Generation
	if out.ExpectedGeneration != nil {
		out.GenerationRelation = "unavailable"
		if out.Generation != nil {
			out.GenerationRelation = "unchanged"
			if *out.Generation > *out.ExpectedGeneration {
				out.GenerationRelation = "advanced"
			}
			if *out.Generation < *out.ExpectedGeneration {
				out.GenerationRelation = "regressed"
			}
		}
	}
	if r.Workflow != "" {
		out.Status = "unsupported_recipient"
		out.Reason = "unsupported_recipient"
		return
	}
	observed := r.Execution
	if r.Recovery != nil && r.Recovery.Latest != nil {
		observed = r.Recovery.Latest
	}
	var ack struct {
		ID string `json:"id"`
	}
	if out.Attempt.Status == "queued" && json.Unmarshal(out.Attempt.Result, &ack) == nil && commitSubscriberUUID.MatchString(ack.ID) {
		if r.Execution != nil && strings.EqualFold(r.Execution.ID, ack.ID) {
			observed = r.Execution
			out.Attribution = "matches_acknowledged_execution"
		}
		if r.Recovery != nil && r.Recovery.Latest != nil && strings.EqualFold(r.Recovery.Latest.ID, ack.ID) {
			observed = r.Recovery.Latest
			out.Attribution = "matches_acknowledged_execution"
		}
	}
	state := r.Routing.State
	if observed != nil {
		state = observed.State
	}
	if observed == nil && len(r.Cancellation) > 0 && string(r.Cancellation) != "null" {
		state = "cancelled"
	}
	if observed == nil && r.ExecutionUnavailable != "" && state != "cancelled" {
		out.Status = "unresolved"
		out.Reason = r.ExecutionUnavailable
		return
	}
	out.Reason = state
	switch state {
	case "completed":
		out.Status = "completed"
	case "pending", "processing", "dispatching":
		out.Status = "active"
	case "failed", "dead_letter", "cancelled", "expired", "superseded":
		out.Status = "failed"
	default:
		out.Status = "unresolved"
		if state == "" {
			out.Reason = "evidence_unavailable"
		}
	}
}

const CommitRecoveryStatusMax = 50
