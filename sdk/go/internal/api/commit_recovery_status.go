package api

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

// CommitRecoveryStatusSelection binds observations to an acceptance and reviewed action.
type CommitRecoveryStatusSelection struct {
	Publication CommitEventPublication `json:"publication"`
	Reviewed    CommitRecoveryPlanItem `json:"reviewed"`
}

// CommitRecoveryStatusResult samples delivery evidence without submitting recovery.
type CommitRecoveryStatusResult struct {
	SubscriptionID string                     `json:"subscription_id,omitempty"`
	Remaining      int                        `json:"remaining"`
	SourceID       string                     `json:"source_id"`
	Items          []CommitRecoveryStatusItem `json:"items"`
	Counts         map[string]int             `json:"counts"`
	Complete       bool                       `json:"complete"`
}
type CommitRecoveryStatusItem struct {
	Selection          *CommitRecoveryStatusSelection `json:"selection,omitempty"`
	Attempt            CommitRecoveryApplyItem        `json:"attempt"`
	Status             string                         `json:"status"`
	Reason             string                         `json:"reason,omitempty"`
	Attribution        string                         `json:"attribution"`
	ObservedAt         *time.Time                     `json:"observed_at,omitempty"`
	ExpectedGeneration *int64                         `json:"expected_generation,omitempty"`
	Generation         *int64                         `json:"generation,omitempty"`
	GenerationRelation string                         `json:"generation_relation,omitempty"`
	Recipient          json.RawMessage                `json:"recipient,omitempty"`
}

// GetCommitRecoveryStatus keeps unknown attempt outcomes unknown, even if delivery advanced.
// It reads only guarded receipt evidence and never follows saved action URLs.
func (c *Client) GetCommitRecoveryStatus(ctx context.Context, reviewed CommitRecoveryPlan, applied CommitRecoveryApplyResult, limit, pages int) (CommitRecoveryStatusResult, error) {
	return c.getCommitRecoveryStatus(ctx, reviewed, applied, nil, limit, pages)
}

// ResumeCommitRecoveryStatus preserves successful observations and retries unread items before failed reads.
func (c *Client) ResumeCommitRecoveryStatus(ctx context.Context, reviewed CommitRecoveryPlan, applied CommitRecoveryApplyResult, previous CommitRecoveryStatusResult, limit, pages int) (CommitRecoveryStatusResult, error) {
	return c.getCommitRecoveryStatus(ctx, reviewed, applied, &previous, limit, pages)
}

func (c *Client) getCommitRecoveryStatus(ctx context.Context, reviewed CommitRecoveryPlan, applied CommitRecoveryApplyResult, previous *CommitRecoveryStatusResult, limit, pages int) (CommitRecoveryStatusResult, error) {
	out := CommitRecoveryStatusResult{SourceID: reviewed.Report.SourceID, Items: []CommitRecoveryStatusItem{}, Counts: map[string]int{}}
	raw, err := json.Marshal(struct {
		Plan     CommitRecoveryPlan
		Applied  CommitRecoveryApplyResult
		Previous *CommitRecoveryStatusResult
	}{reviewed, applied, previous})
	if err != nil {
		return out, err
	}
	var saved struct {
		Plan     CommitRecoveryPlan
		Applied  CommitRecoveryApplyResult
		Previous *CommitRecoveryStatusResult
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

	out.SubscriptionID = plan.Report.SubscriptionID
	for _, attempt := range result.Items {
		selected := selection[strings.ToLower(attempt.EventID+":"+attempt.SubscriptionID)]
		publication := publications[strings.ToLower(attempt.EventID)].Acceptance.Publication
		if publication == nil {
			return out, ErrValidation("saved publication identity mismatch")
		}
		out.Items = append(out.Items, CommitRecoveryStatusItem{
			Attempt: attempt, Status: "not_observed", Reason: "status_budget", Attribution: "unproven",
			ExpectedGeneration: commitRoutingGeneration(selected.Action),
			Selection:          &CommitRecoveryStatusSelection{Publication: *publication, Reviewed: selected},
		})
	}
	if saved.Previous != nil {
		for _, row := range saved.Previous.Items {
			if row.Selection == nil {
				return out, ErrValidation("saved status lacks selection snapshots; run recovery-status without --resume")
			}
		}
		if !strings.EqualFold(saved.Previous.SourceID, out.SourceID) || !strings.EqualFold(saved.Previous.SubscriptionID, out.SubscriptionID) || len(saved.Previous.Items) != len(out.Items) {
			return out, ErrValidation("saved status selection mismatch")
		}
		for i, row := range saved.Previous.Items {
			expected := out.Items[i]
			if !sameRecoveryStatusSelection(*row.Selection, *expected.Selection) || !sameRecoveryStatusJSON(row.Attempt, expected.Attempt) || !reflect.DeepEqual(row.ExpectedGeneration, expected.ExpectedGeneration) {
				return out, ErrValidation("saved status does not match the reviewed plan and apply result")
			}
			switch row.Status {
			case "completed", "active", "failed", "unresolved", "unsupported_recipient", "read_error", "not_observed":
			default:
				return out, ErrValidation("invalid saved status observation")
			}
			if row.ObservedAt != nil && row.ObservedAt.IsZero() {
				return out, ErrValidation("invalid saved observation timestamp")
			}
			if row.Attribution != "unproven" && row.Attribution != "matches_acknowledged_execution" {
				return out, ErrValidation("invalid saved attribution")
			}
			if row.Attribution == "matches_acknowledged_execution" && row.Attempt.Status != "queued" {
				return out, ErrValidation("invalid acknowledged attribution")
			}
			out.Items[i] = row
		}
	}
	recountCommitRecoveryStatus(&out)
	if err = c.validateCommitReportResume(ctx, plan.Report); err != nil {
		out.Complete = false
		return out, err
	}
	pending := []int{}
	for _, status := range []string{"not_observed", "read_error"} {
		for i, row := range out.Items {
			if row.Status == status {
				pending = append(pending, i)
			}
		}
	}
	var firstErr error
	for used, index := range pending {
		if used >= limit {
			break
		}
		if ctx.Err() != nil {
			firstErr = ctx.Err()
			break
		}
		old := out.Items[index]
		row := CommitRecoveryStatusItem{Attempt: old.Attempt, Selection: old.Selection, ExpectedGeneration: old.ExpectedGeneration, Status: "unresolved", Reason: "evidence_unavailable", Attribution: "unproven"}
		original := publications[strings.ToLower(row.Attempt.EventID)]
		original.Subscribers = []CommitDeliveryReportSubscriber{}
		current, readErr := c.RefreshCommitDeliveryReport(ctx, CommitDeliveryReport{SourceID: plan.Report.SourceID, SubscriptionID: strings.ToLower(row.Attempt.SubscriptionID), Items: []CommitDeliveryReportItem{original}}, pages)
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
			if firstErr == nil {
				firstErr = readErr
			}
		}
		out.Items[index] = row
	}
	if ctx.Err() != nil {
		if firstErr == nil {
			firstErr = ctx.Err()
		}
		for i := range out.Items {
			if out.Items[i].Status == "not_observed" {
				out.Items[i].Reason = "interrupted"
			}
		}
	}
	recountCommitRecoveryStatus(&out)
	if firstErr != nil {
		out.Complete = false
	}
	return out, firstErr
}

func recountCommitRecoveryStatus(out *CommitRecoveryStatusResult) {
	out.Counts = map[string]int{}
	out.Remaining = 0
	for _, row := range out.Items {
		out.Counts[row.Status]++
		if row.Status == "not_observed" || row.Status == "read_error" {
			out.Remaining++
		}
	}
	out.Complete = out.Remaining == 0
}

func sameRecoveryStatusJSON(a, b any) bool {
	decode := func(value any) (any, error) {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var result any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		err = decoder.Decode(&result)
		return result, err
	}
	left, leftErr := decode(a)
	right, rightErr := decode(b)
	return leftErr == nil && rightErr == nil && reflect.DeepEqual(left, right)
}

func sameRecoveryStatusSelection(a, b CommitRecoveryStatusSelection) bool {
	if !a.Publication.AcceptedAt.Equal(b.Publication.AcceptedAt) {
		return false
	}
	a.Publication.AcceptedAt = b.Publication.AcceptedAt
	return sameRecoveryStatusJSON(a, b)
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
