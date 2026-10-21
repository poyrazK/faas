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
	if eventID == "" || !m.accounts[memory.source.AccountID].MayDeploy() {
		return nil
	}
	for _, hook := range m.appWebhooks {
		if hook.AccountID != memory.source.AccountID || hook.Scope != AppWebhookScopeAccount || !hook.Enabled || !slices.Contains(hook.EventFilter, string(AppWebhookEventEnvironmentGitOpsChanged)) {
			continue
		}
		id := newID()
		m.appWebhookDeliveries[id] = AppWebhookDelivery{ID: id, WebhookID: hook.ID, AccountID: hook.AccountID,
			Event: AppWebhookEventEnvironmentGitOpsChanged, Payload: slices.Clone(payload), Status: AppWebhookDeliveryPending,
			NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
	}
	return nil
}
