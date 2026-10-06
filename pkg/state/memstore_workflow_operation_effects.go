package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
)

var _ ManagedWorkflowStepCommitter = (*MemStore)(nil)

func (m *MemStore) CommitManagedWorkflowStep(ctx context.Context, input ManagedWorkflowStepCommit) error {
	commit, err := prepareManagedWorkflowStepCommit(input)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	run, ok := m.workflowRuns[commit.RunID]
	if !ok {
		return ErrWorkflowRunNotFound
	}
	if run.Status != WorkflowRunStatusRunning {
		return ErrWorkflowNotRunning
	}
	steps, ok := m.workflowSteps[commit.RunID]
	if !ok {
		return ErrWorkflowStepNotFound
	}
	step, ok := steps[commit.StepName]
	if !ok {
		return ErrWorkflowStepNotFound
	}
	if step.Status != WorkflowStepStatusRunning || step.Attempt != commit.Attempt {
		return workflowEffectConflict("workflow step attempt is no longer active")
	}
	attemptKey := workflowStepAttemptKey{runID: commit.RunID, stepName: commit.StepName, attempt: commit.Attempt}
	attempt, ok := m.workflowStepAttempts[attemptKey]
	if !ok {
		return ErrWorkflowAttemptNotFound
	}
	if attempt.Status != WorkflowAttemptStatusRunning {
		return workflowEffectConflict("workflow attempt is no longer active")
	}

	var staged []workflowOperationStoredEffect
	var deliveries []AppWebhookDelivery
	now := time.Now().UTC()
	if m.exclusiveNow != nil {
		now = m.exclusiveNow().UTC()
	}
	if len(commit.Effects) > 0 {
		app, exists := m.apps[run.AppID]
		account, accountExists := m.accounts[app.AccountID]
		if !exists || app.Status == AppDeleted || !accountExists || account.Status != AccountActive {
			return ErrOperationEffectDestination
		}
		if run.PlatformTenantID != "" {
			tenant, tenantExists := m.platformTenants[run.PlatformTenantID]
			linked := false
			for surfaceID, linkedTenantID := range m.platformTenantBySurface {
				surface := m.tenantSurfaces[surfaceID]
				if canonicalMemUUID(linkedTenantID) == canonicalMemUUID(run.PlatformTenantID) &&
					canonicalMemUUID(surface.AccountID) == canonicalMemUUID(app.AccountID) &&
					canonicalMemUUID(surface.AppID) == canonicalMemUUID(run.AppID) && surface.Status == SurfaceStatusActive {
					linked = true
					break
				}
			}
			if !tenantExists || canonicalMemUUID(tenant.AccountID) != canonicalMemUUID(app.AccountID) || !linked {
				return ErrOperationEffectDestination
			}
		}
		staged = make([]workflowOperationStoredEffect, 0, len(commit.Effects))
		deliveries = make([]AppWebhookDelivery, 0, len(commit.Effects))
		for _, effect := range commit.Effects {
			var hook AppWebhook
			for id, candidate := range m.appWebhooks {
				if canonicalMemUUID(id) == canonicalMemUUID(effect.WebhookID) {
					hook = candidate
					break
				}
			}
			if !managedWorkflowEffectDestinationMatches(app.AccountID, run.AppID, run.PlatformTenantID, hook) {
				return ErrOperationEffectDestination
			}
			id := workflowOperationEffectID(commit.OperationID, effect.Name)
			if _, err := uuid.Parse(id); err != nil {
				return ErrInvalidArgument
			}
			if _, exists := m.appWebhookDeliveries[id]; exists {
				return workflowEffectConflict("workflow effect delivery identity already exists")
			}
			for _, recorded := range m.workflowOperationEffects {
				for _, prior := range recorded {
					if prior.OperationID == commit.OperationID && prior.Effect.Name == effect.Name {
						return workflowEffectConflict("workflow effect identity already exists")
					}
				}
			}
			body, err := workflowOperationEffectBody(commit.OperationID, run.AppID, run.PlatformTenantID, int64(commit.Attempt), effect)
			if err != nil {
				return ErrInvalidArgument
			}
			record := apiOperationEffectRecord(id, effect, int64(commit.Attempt), hook.ID)
			staged = append(staged, workflowOperationStoredEffect{OperationID: commit.OperationID, Effect: effect, Record: record})
			deliveries = append(deliveries, AppWebhookDelivery{
				ID: id, WebhookID: hook.ID, AppID: run.AppID, AccountID: app.AccountID,
				Event: OperationEffectEvent, Payload: body, Status: AppWebhookDeliveryPending,
				NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
			})
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	if m.workflowOperationEffects == nil {
		m.workflowOperationEffects = make(map[workflowStepAttemptKey][]workflowOperationStoredEffect)
	}
	if len(staged) > 0 {
		m.workflowOperationEffects[attemptKey] = staged
		for _, delivery := range deliveries {
			m.appWebhookDeliveries[delivery.ID] = delivery
		}
	}
	now = time.Now().UTC()
	step.Status = WorkflowStepStatusSucceeded
	step.Attempt = commit.Attempt
	step.Output = cloneWorkflowJSON(commit.Output)
	step.Error = nil
	step.NextRetryAt = nil
	step.FinishedAt = &now
	steps[commit.StepName] = step
	m.workflowSteps[commit.RunID] = steps
	attempt.Status = WorkflowAttemptStatusSucceeded
	httpStatus := commit.HTTPStatus
	attempt.HTTPStatus = &httpStatus
	attempt.FinishedAt = &now
	attempt.NextAttemptAt = nil
	attempt.Error = nil
	m.workflowStepAttempts[attemptKey] = attempt
	run.CurrentStep = &step.StepName
	run.UpdatedAt = now
	m.workflowRuns[commit.RunID] = run
	return nil
}

func apiOperationEffectRecord(id string, effect exclusivework.Effect, generation int64, webhookID string) api.OperationEffectRecord {
	return api.OperationEffectRecord{
		ID: id, Name: effect.Name, Generation: generation, WebhookID: webhookID,
		DeliveryID: id, Type: effect.Type, Status: string(AppWebhookDeliveryPending),
	}
}
