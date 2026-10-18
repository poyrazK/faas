package state

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func initialReviewReadyPayload(r api.WorkflowInitialRecoveryReview, now time.Time) api.WorkflowRecoveryReviewReadyWebhookPayload {
	path := "/v1/apps/" + url.PathEscape(r.App) + "/initial-recovery-reviews/" + r.ID
	command := "gregale automations failure-recovery-review --app " + strconv.Quote(r.App) + " --review " + r.ID
	return api.WorkflowRecoveryReviewReadyWebhookPayload{InitialFailureID: r.Failure.ID, EventID: recoveryReviewNotificationID(r.ID), ReviewID: r.ID, AppID: r.AppID, App: r.App, PolicyVersion: r.PolicyVersion, PreparedAt: r.PreparedAt, ObservedAt: now, ReviewURL: path, StatusURL: path, ReviewCommand: command, ExportCommand: command + " --json", Targets: []api.WorkflowRecoveryPolicyDecision{{RunID: r.Failure.RunID, ResumeCount: 0, Trigger: "initial_failure", Action: "initial_recovery", Outcome: "prepared", FailureStatus: r.Failure.FailureStatus}}}
}
func initialReviewNotificationAllowed(ctx context.Context, s WorkflowRecoveryReviewInspector, row AppWebhookDelivery, payload api.WorkflowRecoveryReviewReadyWebhookPayload, now time.Time) (bool, string, error) {
	store, ok := s.(WorkflowInitialRecoveryReviewStore)
	if !ok {
		return false, "initial_review_unavailable", nil
	}
	r, err := store.GetWorkflowInitialRecoveryReview(ctx, row.AccountID, row.AppID, payload.ReviewID)
	if errors.Is(err, ErrNotFound) {
		return false, "review_not_retained", nil
	}
	if err != nil {
		return false, "", err
	}
	if r.Status != "prepared" || r.Reason != "eligible" || !r.ExpiresAt.After(now) {
		return false, "review_not_ready", nil
	}
	n := r.Notification
	if n == nil || n.Status != "queued" || n.EventID != payload.EventID || !recoveryPolicyHas(n.ReceiverWebhookIDs, row.WebhookID) {
		return false, "capture_mismatch", nil
	}
	if !reflect.DeepEqual(initialReviewReadyPayload(r, n.CapturedAt), payload) {
		return false, "payload_mismatch", nil
	}
	return true, "ready", nil
}
