package state

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"net/url"
	"time"
)

type WorkflowRecoveryAutomaticActionOperatorStore interface {
	SuppressWorkflowRecoveryAutomaticAction(context.Context, string, string, string, string, api.SuppressWorkflowRecoveryAutomaticActionRequest) (api.WorkflowRecoveryAutomaticAction, error)
}
type WorkflowRecoveryAutomaticBlockedStore interface {
	ProcessNextWorkflowRecoveryAutomaticBlockedNotification(context.Context, time.Time) (bool, error)
}

func validateAutomaticSuppression(account, app, review, action string, r api.SuppressWorkflowRecoveryAutomaticActionRequest) error {
	for _, id := range []string{app, review, action} {
		if err := eventRecoveryIDs(account, id); err != nil {
			return err
		}
	}
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	return nil
}
func automaticSuppressionReplay(e api.WorkflowRecoveryAutomaticAction, r api.SuppressWorkflowRecoveryAutomaticActionRequest) bool {
	return e.Status == "suppressed" && e.Suppression != nil && e.Suppression.Reason == r.Reason
}
func automaticBlockedEligible(p api.WorkflowRecoveryPolicyResponse, e api.WorkflowRecoveryAutomaticAction, version int64) bool {
	return p.Policy.Enabled && p.Policy.Mode == "automatic_retry" && p.AutomaticExecution != nil && p.AutomaticExecution.PausedAt == nil && e.BlockedNotification == nil && (e.Status == "pending" && e.Reason == "evidence_unknown" && version == p.Version || e.Status == "applied" && e.Result == nil)
}
func automaticBlockedEvidence(ctx context.Context, s WorkflowRecoveryReviewInspector, account, app, review string, e api.WorkflowRecoveryAutomaticAction, now time.Time) (bool, error) {
	if e.Status == "pending" {
		retained, err := s.GetWorkflowRecoveryPolicyReview(ctx, account, app, review)
		if err != nil {
			return false, fmt.Errorf("inspect blocked retry decision: %w", err)
		}
		if retained.ReviewDecision != nil && retained.ReviewDecision.Decision == "dismissed" {
			return false, nil
		}
	}
	status, err := InspectWorkflowRecoveryReview(ctx, s, account, app, review, now)
	if err != nil {
		return false, fmt.Errorf("inspect blocked automatic retry: %w", err)
	}
	for _, a := range status.Actions {
		if a.Position == e.Position && a.Kind == "retry" && a.ChildJobID == e.ChildJobID {
			return a.Outcome == "unknown" && !a.Complete && (e.Status == "applied" || a.Status == "unknown" || a.Reason == "run_not_retained"), nil
		}
	}
	return false, nil
}
func automaticBlockedPayload(app, slug, review string, version int64, e api.WorkflowRecoveryAutomaticAction, now time.Time) api.WorkflowRecoveryQueueBlockedWebhookPayload {
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:recovery-action:"+e.ActionID+":blocked")).String()
	base := "/v1/apps/" + url.PathEscape(slug) + "/recovery-policy"
	return api.WorkflowRecoveryQueueBlockedWebhookPayload{EventID: id, AppID: app, App: slug, ReviewID: review, ActionID: e.ActionID, PolicyVersion: version, Position: e.Position, ChildJobID: e.ChildJobID, Status: e.Status, Reason: "evidence_unknown", ObservedAt: now, QueueURL: base + "/queue", ReviewStatusURL: base + "/reviews/" + review + "/status"}
}
func automaticBlockedReceipt(payload api.WorkflowRecoveryQueueBlockedWebhookPayload, receivers []string) api.WorkflowRecoveryAutomaticBlockedNotification {
	status := "queued"
	if len(receivers) == 0 {
		status = "no_receivers"
	}
	return api.WorkflowRecoveryAutomaticBlockedNotification{EventID: payload.EventID, Status: status, Reason: payload.Reason, CapturedAt: payload.ObservedAt, ReceiverWebhookIDs: receivers}
}
