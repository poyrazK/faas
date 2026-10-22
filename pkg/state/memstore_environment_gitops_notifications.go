package state

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) environmentGitNotificationLocked(memory *environmentGitOpsMemory, axis, status, runID, code string, plan api.EnvironmentGitOpsPlan, now time.Time) error {
	if axis == "drift" && plan.Hash != "" && memory.source.IntentVersion != plan.ObservedVersion {
		return nil
	}
	project := m.projects[memory.source.ProjectID]
	if axis == "source" || plan.CommitSHA == "" {
		plan.CommitSHA = memory.revisions[memory.source.ApprovedRevisionID].CommitSHA
	}
	next, payload, eventID, err := prepareEnvironmentGitNotification(memory.notifications, memory.source, project.Slug, axis, status, runID, code, plan, now)
	if err != nil {
		return err
	}
	memory.notifications = next
	m.enqueueEnvironmentGitNotificationLocked(memory, environmentGitNotificationEvent{id: eventID, body: payload}, now)
	return nil
}

func (m *MemStore) environmentGitRolloutNotificationLocked(memory *environmentGitOpsMemory, evidence *EnvironmentWorkloadActivationEvidence, runID string, now time.Time) error {
	next, events, err := prepareEnvironmentGitRolloutNotifications(memory.notifications, memory.source, memory.revisions[memory.source.ApprovedRevisionID],
		m.projects[memory.source.ProjectID].Slug, runID, evidence, now)
	if err != nil {
		return err
	}
	memory.notifications = next
	for _, event := range events {
		m.enqueueEnvironmentGitNotificationLocked(memory, event, now)
	}
	return nil
}

func (m *MemStore) enqueueEnvironmentGitNotificationLocked(memory *environmentGitOpsMemory, event environmentGitNotificationEvent, now time.Time) {
	if event.id == "" || !m.accounts[memory.source.AccountID].MayDeploy() {
		return
	}
	for _, hook := range m.appWebhooks {
		if hook.AccountID != memory.source.AccountID || hook.Scope != AppWebhookScopeAccount || !hook.Enabled || !slices.Contains(hook.EventFilter, string(AppWebhookEventEnvironmentGitOpsChanged)) {
			continue
		}
		id := newID()
		m.appWebhookDeliveries[id] = AppWebhookDelivery{ID: id, WebhookID: hook.ID, AccountID: hook.AccountID,
			Event: AppWebhookEventEnvironmentGitOpsChanged, Payload: slices.Clone(event.body), Status: AppWebhookDeliveryPending,
			NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
	}
}
