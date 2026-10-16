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
	if e.Status != "pending" && e.Status != "applied" {
		return false
	}
	if n := e.BlockedNotification; n != nil && n.State != "recovered" {
		return true
	}
	return p.Policy.Enabled && api.WorkflowRecoveryPolicyAutomaticMode(p.Policy.Mode) && p.AutomaticExecution != nil && p.AutomaticExecution.PausedAt == nil && (e.Status == "pending" && e.Reason == "evidence_unknown" && version == p.Version || e.Status == "applied" && e.Result == nil)
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
	return api.WorkflowRecoveryAutomaticBlockedNotification{State: payload.State, EvidenceKind: payload.EvidenceKind, DiagnosisReason: payload.DiagnosisReason, EventID: payload.EventID, Status: status, Reason: payload.Reason, CapturedAt: payload.ObservedAt, ReceiverWebhookIDs: receivers}
}
