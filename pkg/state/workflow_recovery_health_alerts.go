package state

import (
	"context"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type WorkflowRecoveryHealthAlertStore interface {
	ProcessNextWorkflowRecoveryHealthAlert(context.Context, time.Time) (bool, error)
}

func recoveryHealthAlertEnabled(p api.WorkflowRecoveryPolicy) bool {
	return p.HealthAlerts != nil && p.HealthAlerts.Enabled
}
func recoveryHealthIncidentOpen(i *api.WorkflowRecoveryHealthIncident) bool {
	return i != nil && i.ClosedAt == nil
}

func recoveryHealthTransition(previous *api.WorkflowRecoveryHealthIncident, h api.WorkflowRecoveryPolicyHealth, enabled bool) (*api.WorkflowRecoveryHealthIncident, AppWebhookEvent) {
	i := recoveryPolicyClone(previous)
	now := h.Queue.ObservedAt
	if !enabled {
		if recoveryHealthIncidentOpen(i) {
			i.ClosedAt = &now
			i.Status = "inactive"
			i.Reason = "health_alerts_disabled"
			i.ObservedAt = now
		}
		return i, ""
	}
	if h.Status == "unknown" {
		if recoveryHealthIncidentOpen(i) {
			i.ObservedAt = now
			i.Status = h.Status
			i.Reason = h.Reason
			if i.AlertNotification == nil {
				i.CandidateSince = now
			}
		}
		return i, ""
	}
	if h.Status == "healthy" || h.Status == "inactive" {
		if !recoveryHealthIncidentOpen(i) {
			return i, ""
		}
		i.ClosedAt = &now
		i.Status = h.Status
		i.Reason = h.Reason
		i.ObservedAt = now
		if i.AlertNotification != nil {
			return i, AppWebhookEventRecoveryHealthRecovered
		}
		return i, ""
	}
	if !recoveryHealthIncidentOpen(i) {
		i = &api.WorkflowRecoveryHealthIncident{EpisodeID: uuid.NewString(), OpenedAt: now, CandidateSince: now}
	}
	i.ObservedAt = now
	i.Status = h.Status
	i.Reason = h.Reason
	if i.AlertNotification == nil && now.Sub(i.CandidateSince) >= time.Duration(h.Queue.Policy.Policy.HealthAlerts.AlertAfterSeconds)*time.Second {
		return i, AppWebhookEventRecoveryHealthAlert
	}
	return i, ""
}
func recoveryHealthPayload(i *api.WorkflowRecoveryHealthIncident, h api.WorkflowRecoveryPolicyHealth, slug string, event AppWebhookEvent) api.WorkflowRecoveryHealthWebhookPayload {
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:recovery-health:"+i.EpisodeID+":"+string(event))).String()
	return api.WorkflowRecoveryHealthWebhookPayload{EventID: id, EpisodeID: i.EpisodeID, AppID: h.Queue.Policy.AppID, App: slug, PolicyVersion: h.Queue.Policy.Version, Status: h.Status, Reason: h.Reason, OpenedAt: i.OpenedAt, ObservedAt: h.Queue.ObservedAt, Pending: h.Queue.Pending, Outstanding: h.Queue.Outstanding, QueueTruncated: h.Queue.Truncated, HealthURL: "/v1/apps/" + url.PathEscape(slug) + "/recovery-policy/health"}
}
func recoveryHealthCapture(i *api.WorkflowRecoveryHealthIncident, payload api.WorkflowRecoveryHealthWebhookPayload, event AppWebhookEvent, receivers []string) {
	status := "queued"
	if len(receivers) == 0 {
		status = "no_receivers"
	}
	r := &api.WorkflowRecoveryHealthNotification{EventID: payload.EventID, Status: status, CapturedAt: payload.ObservedAt, ReceiverWebhookIDs: receivers}
	if event == AppWebhookEventRecoveryHealthAlert {
		i.AlertNotification = r
	} else {
		i.RecoveryNotification = r
	}
}
func recoveryHealthReceiverAllowed(i *api.WorkflowRecoveryHealthIncident, event AppWebhookEvent, id string) bool {
	if event == AppWebhookEventRecoveryHealthAlert {
		return true
	}
	if i.AlertNotification != nil {
		for _, original := range i.AlertNotification.ReceiverWebhookIDs {
			if sameMemUUID(original, id) {
				return true
			}
		}
	}
	return false
}
