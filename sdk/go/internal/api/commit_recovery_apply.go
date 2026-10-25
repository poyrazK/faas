package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// CommitRecoveryApplyResult describes attempts, never business-effect completion.
type CommitRecoveryApplyResult struct {
	SourceID string                    `json:"source_id"`
	Items    []CommitRecoveryApplyItem `json:"items"`
	Counts   map[string]int            `json:"counts"`
	Complete bool                      `json:"complete"`
}
type CommitRecoveryApplyItem struct {
	EventID        string          `json:"event_id"`
	SubscriptionID string          `json:"subscription_id"`
	Status         string          `json:"status"`
	Reason         string          `json:"reason,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
}

// ApplyCommitRecovery submits only reviewed, freshly revalidated execution recovery actions.
// Routing actions require the precise acceptance guard in the fresh server offer.
// Ambiguous writes stop the batch without retrying. limit bounds reviewed eligible items.
func (c *PreviewClient) ApplyCommitRecovery(ctx context.Context, reviewed CommitRecoveryPlan, limit, pages int) (CommitRecoveryApplyResult, error) {
	out := CommitRecoveryApplyResult{SourceID: reviewed.Report.SourceID, Items: []CommitRecoveryApplyItem{}, Counts: map[string]int{}}
	raw, err := json.Marshal(reviewed)
	if err != nil {
		return out, err
	}
	var plan CommitRecoveryPlan
	if err = json.Unmarshal(raw, &plan); err != nil {
		return out, err
	}
	if limit < 1 || limit > CommitRecoveryApplyMax || pages < 1 || pages > CommitDeliveryReportPagesMax || len(plan.Report.Items) > CommitPublicationHistoryPageMax || len(plan.Items) > CommitPublicationHistoryPageMax*AppEventPublishStatusRecipientsMax*CommitDeliveryReportPagesMax {
		return out, ErrValidation("invalid recovery apply budget or plan")
	}
	if err = c.validateCommitReportResume(ctx, plan.Report); err != nil {
		return out, err
	}
	publications := map[string]CommitDeliveryReportItem{}
	for _, item := range plan.Report.Items {
		publications[strings.ToLower(item.Acceptance.EventID)] = item
	}
	seen := map[string]bool{}
	for _, item := range plan.Items {
		_, exists := publications[strings.ToLower(item.EventID)]
		key := strings.ToLower(item.EventID + ":" + item.SubscriptionID)
		if !exists || !commitSubscriberUUID.MatchString(item.SubscriptionID) || seen[key] || (plan.Report.SubscriptionID != "" && !strings.EqualFold(plan.Report.SubscriptionID, item.SubscriptionID)) || (item.Status != "eligible" && item.Status != "skipped") || (item.Status == "eligible" && item.Action == nil) {
			return out, ErrValidation("invalid reviewed recovery selection")
		}
		seen[key] = true
	}
	used := 0
	stopped := false
	var stopErr error
	for _, item := range plan.Items {
		result := CommitRecoveryApplyItem{EventID: item.EventID, SubscriptionID: item.SubscriptionID, Status: "skipped", Reason: "not_reviewed_eligible"}
		if stopped {
			result.Status = "not_attempted"
			result.Reason = "batch_stopped"
		} else if item.Status == "eligible" {
			if used >= limit {
				result.Status = "not_attempted"
				result.Reason = "apply_budget"
			} else {
				used++
				if err = ctx.Err(); err != nil {
					stopped = true
					stopErr = err
					result.Status = "not_attempted"
					result.Reason = "interrupted"
				} else {
					original := publications[strings.ToLower(item.EventID)]
					saved := CommitDeliveryReport{SourceID: plan.Report.SourceID, SubscriptionID: strings.ToLower(item.SubscriptionID), Items: []CommitDeliveryReportItem{original}}
					// Discard saved subscriber evidence before selecting this one filter.
					saved.Items[0].Subscribers = []CommitDeliveryReportSubscriber{}
					current, readErr := c.PlanCommitRecovery(ctx, saved, pages)
					if readErr != nil {
						result.Status = "read_error"
						result.Reason = "eligibility_read_failed"
						if ctx.Err() != nil {
							stopped = true
							stopErr = ctx.Err()
						}
					} else if len(current.Items) == 0 {
						result.Reason = "evidence_unavailable"
						if len(current.Report.Items) > 0 {
							result.Reason = current.Report.Items[0].EvidenceStatus
						}
					} else {
						fresh := current.Items[0]
						generationReason := commitRoutingGenerationReason(item.Action, current.Report)
						if generationReason != "" {
							result.Reason = generationReason
						} else if fresh.Status != "eligible" {
							result.Reason = fresh.Reason
						} else if !sameCommitRecoveryAction(item.Action, fresh.Action) {
							result.Reason = "action_changed"
						} else if fresh.Action.Kind == "routing_replay" && !commitRoutingGuardPresent(fresh.Action) {
							result.Reason = "acceptance_guard_unavailable"
						} else {
							// Only fresh planner-validated guarded routing or execution paths reach this write.
							keyIdentity := fresh.Action.Kind + "\n" + fresh.Action.URL
							var body any
							if fresh.Action.Kind == "routing_replay" {
								body = fresh.Action.Body
								keyIdentity += "\n" + original.Acceptance.Publication.Source + "\n" + original.Acceptance.Publication.ID + "\n" + strings.ToLower(item.SubscriptionID) + "\n" + original.Acceptance.Publication.AcceptedAt.UTC().Format(time.RFC3339Nano)
								keyIdentity += fmt.Sprintf("\n%d", *commitRoutingGeneration(fresh.Action))
							}
							key := fmt.Sprintf("commit-recovery-%x", sha256.Sum256([]byte(keyIdentity)))
							var response json.RawMessage
							writeErr := c.doWithIdempotencyKey(ctx, "POST", fresh.Action.URL, body, &response, key)
							if writeErr == nil {
								var receipt struct {
									ID string `json:"id"`
								}
								if fresh.Action.Kind == "routing_replay" {
									var routed struct {
										EventSource    string `json:"event_source"`
										EventID        string `json:"event_id"`
										SubscriptionID string `json:"subscription_id"`
										State          string `json:"state"`
									}
									if json.Unmarshal(response, &routed) != nil || routed.EventSource != original.Acceptance.Publication.Source || routed.EventID != original.Acceptance.Publication.ID || !strings.EqualFold(routed.SubscriptionID, item.SubscriptionID) || routed.State != "pending" {
										writeErr = fmt.Errorf("routing recovery acknowledgement is unavailable")
									}
								} else if json.Unmarshal(response, &receipt) != nil || receipt.ID == "" {
									writeErr = fmt.Errorf("recovery acknowledgement is unavailable")
								}
							}
							if writeErr == nil {
								result.Status = "queued"
								result.Reason = ""
								result.Result = response
							} else {
								var apiErr *APIError
								if errors.As(writeErr, &apiErr) && apiErr.Problem.Status == 409 && apiErr.Problem.Code == "event_replay_generation_changed" {
									result.Status = "skipped"
									result.Reason = "generation_changed"
								} else if errors.As(writeErr, &apiErr) && apiErr.Problem.Status >= 400 && apiErr.Problem.Status < 500 && apiErr.Problem.Status != 408 {
									result.Status = "rejected"
									result.Reason = fmt.Sprintf("http_%d", apiErr.Problem.Status)
								} else {
									result.Status = "unknown"
									result.Reason = "inspect_before_retry"
									stopped = true
									stopErr = writeErr
								}
							}
						}
					}
				}
			}
		}
		out.Items = append(out.Items, result)
		out.Counts[result.Status]++
	}
	out.Complete = !stopped
	return out, stopErr
}

func sameCommitRecoveryAction(a, b *CommitRecoveryPlanAction) bool {
	if a == nil || b == nil || a.Kind != b.Kind || a.Method != b.Method || a.URL != b.URL {
		return false
	}
	var left, right any
	if len(a.Body) > 0 {
		if json.Unmarshal(a.Body, &left) != nil {
			return false
		}
	}
	if len(b.Body) > 0 {
		if json.Unmarshal(b.Body, &right) != nil {
			return false
		}
	}
	return reflect.DeepEqual(left, right)
}

func commitRoutingGuardPresent(action *CommitRecoveryPlanAction) bool {
	var body struct {
		ExpectedAcceptedAt *time.Time `json:"expected_accepted_at"`
	}
	return json.Unmarshal(action.Body, &body) == nil && body.ExpectedAcceptedAt != nil && !body.ExpectedAcceptedAt.IsZero()
}

func commitRoutingGeneration(action *CommitRecoveryPlanAction) *int64 {
	if action == nil || action.Kind != "routing_replay" {
		return nil
	}
	var body struct {
		Generation *int64 `json:"expected_generation"`
	}
	if json.Unmarshal(action.Body, &body) != nil || body.Generation == nil || *body.Generation < 0 {
		return nil
	}
	return body.Generation
}
func commitRoutingGenerationReason(action *CommitRecoveryPlanAction, report CommitDeliveryReport) string {
	if action == nil || action.Kind != "routing_replay" {
		return ""
	}
	expected := commitRoutingGeneration(action)
	if expected == nil {
		return "generation_guard_unavailable"
	}
	if len(report.Items) == 0 || len(report.Items[0].Subscribers) == 0 {
		return "generation_guard_unavailable"
	}
	var recipient struct {
		Routing struct {
			Generation *int64 `json:"generation"`
		} `json:"routing"`
	}
	if json.Unmarshal(report.Items[0].Subscribers[0].Recipient, &recipient) != nil || recipient.Routing.Generation == nil {
		return "generation_guard_unavailable"
	}
	if *expected != *recipient.Routing.Generation {
		return "generation_changed"
	}
	return ""
}

const CommitRecoveryApplyMax = 50
