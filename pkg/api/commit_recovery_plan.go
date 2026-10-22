package api

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// CommitRecoveryPlan is a read-only live eligibility sample, not an executable authorization.
// Report retains publication coverage and acceptance guards, including partial reads.
type CommitRecoveryPlan struct {
	PublicationIssues map[string]string        `json:"publication_issues"`
	Report            CommitDeliveryReport     `json:"report"`
	Items             []CommitRecoveryPlanItem `json:"items"`
	Counts            map[string]int           `json:"counts"`
}

type CommitRecoveryPlanItem struct {
	EventID        string                    `json:"event_id"`
	SubscriptionID string                    `json:"subscription_id"`
	Status         string                    `json:"status"`
	Reason         string                    `json:"reason,omitempty"`
	Action         *CommitRecoveryPlanAction `json:"action,omitempty"`
}

// CommitRecoveryPlanAction describes an existing server-offered recovery endpoint.
type CommitRecoveryPlanAction struct {
	Kind   string          `json:"kind"`
	Method string          `json:"method"`
	URL    string          `json:"url"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// PlanCommitRecovery refreshes the saved selection within the recipient page budget.
// It never executes actions, follows action URLs, or reads replay history.
func (c *Client) PlanCommitRecovery(ctx context.Context, saved CommitDeliveryReport, pages int) (CommitRecoveryPlan, error) {
	report, err := c.RefreshCommitDeliveryReport(ctx, saved, pages)
	out := CommitRecoveryPlan{PublicationIssues: map[string]string{}, Report: report, Items: []CommitRecoveryPlanItem{}, Counts: map[string]int{}}
	for _, item := range report.Items {
		if item.EvidenceStatus != "available" {
			out.PublicationIssues[item.Acceptance.EventID] = item.EvidenceStatus
		}
		if item.EvidenceStatus != "available" && item.EvidenceStatus != "partial" && item.EvidenceStatus != "read_error" {
			continue
		}
		for _, row := range item.Subscribers {
			status, reason, action := commitRecoveryEligibility(row)
			out.Items = append(out.Items, CommitRecoveryPlanItem{EventID: item.Acceptance.EventID, SubscriptionID: row.SubscriptionID, Status: status, Reason: reason, Action: action})
			out.Counts[status]++
		}
	}
	return out, err
}

var commitRecoverySlug = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)

func commitRecoveryEligibility(row CommitDeliveryReportSubscriber) (string, string, *CommitRecoveryPlanAction) {
	skip := func(reason string) (string, string, *CommitRecoveryPlanAction) { return "skipped", reason, nil }
	if row.Status == "not_targeted" {
		return skip("not_targeted")
	}
	var r struct {
		Routing struct {
			Generation *int64 `json:"generation"`
		} `json:"routing"`
		WorkflowName         string          `json:"workflow_name"`
		AppSlug              string          `json:"app_slug"`
		ExecutionUnavailable string          `json:"execution_unavailable"`
		Cancellation         json.RawMessage `json:"cancellation"`
		Execution            *struct {
			InvocationID string `json:"invocation_id"`
			State        string `json:"state"`
		} `json:"execution"`
		Recovery *struct {
			LatestReplay *struct {
				InvocationID string `json:"invocation_id"`
				State        string `json:"state"`
			} `json:"latest_replay"`
		} `json:"recovery"`
		Actions []CommitRecoveryPlanAction `json:"recovery_actions"`
	}
	if len(row.Recipient) == 0 || json.Unmarshal(row.Recipient, &r) != nil {
		return skip("evidence_unavailable")
	}
	if r.WorkflowName != "" {
		return skip("unsupported_recipient")
	}
	execution := r.Execution
	if r.Recovery != nil && r.Recovery.LatestReplay != nil {
		execution = r.Recovery.LatestReplay
	}
	if execution != nil {
		if execution.State == "completed" {
			return skip("completed")
		}
		if execution.State == "pending" || execution.State == "dispatching" {
			return skip("delivery_active")
		}
	}
	if len(r.Actions) == 0 {
		if r.ExecutionUnavailable != "" {
			return skip("evidence_unavailable")
		}
		if len(r.Cancellation) > 0 && string(r.Cancellation) != "null" {
			return skip("cancelled")
		}
		return skip("no_recovery_action")
	}
	if len(r.Actions) != 1 {
		return skip("unsupported_action")
	}
	action := r.Actions[0]
	if action.Method != "POST" {
		return skip("unsupported_action")
	}
	hasBody := len(action.Body) > 0 && string(action.Body) != "null"
	switch action.Kind {
	case "routing_replay":
		var body struct {
			EventSource        string     `json:"event_source"`
			EventID            string     `json:"event_id"`
			SubscriptionID     string     `json:"subscription_id"`
			AllowExpired       bool       `json:"allow_expired"`
			ExpectedGeneration *int64     `json:"expected_generation"`
			ExpectedAcceptedAt *time.Time `json:"expected_accepted_at"`
		}
		if !commitRecoverySlug.MatchString(r.AppSlug) || strings.HasPrefix(r.AppSlug, "tag-") || action.URL != "/v1/apps/"+r.AppSlug+"/event-deliveries:replay-fanout-failure" || !hasBody || json.Unmarshal(action.Body, &body) != nil || body.AllowExpired || body.EventSource != row.Publication.Source || body.EventID != row.Publication.ID || !strings.EqualFold(body.SubscriptionID, row.SubscriptionID) {
			return skip("action_identity_mismatch")
		}
		if body.ExpectedGeneration != nil && (body.ExpectedAcceptedAt == nil || *body.ExpectedGeneration < 0 || r.Routing.Generation == nil || *body.ExpectedGeneration != *r.Routing.Generation) {
			return skip("action_identity_mismatch")
		}
		if body.ExpectedAcceptedAt != nil && !body.ExpectedAcceptedAt.Equal(row.Publication.AcceptedAt) {
			return skip("action_identity_mismatch")
		}
	case "handler_replay", "keyed_handler_replay":
		suffix := "/replay"
		if action.Kind == "keyed_handler_replay" {
			suffix = "/replay-keyed"
		}
		if execution == nil || !commitSubscriberUUID.MatchString(execution.InvocationID) || action.URL != "/v1/invocations/"+execution.InvocationID+suffix || hasBody {
			return skip("action_identity_mismatch")
		}
	case "dead_letter_replay":
		prefix := "/v1/apps/" + r.AppSlug + "/dlq/"
		target := strings.TrimSuffix(strings.TrimPrefix(action.URL, prefix), "/replay")
		if !commitRecoverySlug.MatchString(r.AppSlug) || strings.HasPrefix(r.AppSlug, "tag-") || !commitSubscriberUUID.MatchString(target) || action.URL != prefix+target+"/replay" || hasBody {
			return skip("action_identity_mismatch")
		}
	default:
		return skip("unsupported_action")
	}
	return "eligible", "", &action
}
