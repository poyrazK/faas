package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) StartAutomationLiveSuite(ctx context.Context, appID, name, id string, request api.StartAutomationLiveSuiteRequest, runtimeEnabled bool) (api.AutomationLiveSuite, error) {
	id, plan, err := managedSuitePlan(id, request)
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.AutomationLiveSuite{}, err
	}
	if prior, ok := m.automationLiveSuites[id]; ok {
		if prior.AppID != appID || prior.Name != name {
			return api.AutomationLiveSuite{}, ErrNotFound
		}
		if err := managedSuiteReplay(prior, plan); err != nil {
			return api.AutomationLiveSuite{}, err
		}
		return m.liveSuiteViewLocked(prior)
	}
	if err := validateManagedSuiteCreation(id); err != nil {
		return api.AutomationLiveSuite{}, err
	}
	if err := m.liveSuiteRoomLocked(appID, name); err != nil {
		return api.AutomationLiveSuite{}, err
	}
	current, ok := m.automations[appID+"/"+name]
	if !ok {
		return api.AutomationLiveSuite{}, ErrNotFound
	}
	if err := validateLiveSuiteTarget(plan, current, m.automationDeploymentLocked(appID).ID); err != nil {
		return api.AutomationLiveSuite{}, err
	}
	now := time.Now().UTC()
	record := automationLiveSuiteRecord{ID: id, AppID: appID, Name: name, Plan: plan, Managed: true, CreatedAt: now, UpdatedAt: now}
	view, err := m.advanceManagedSuiteLocked(ctx, record, runtimeEnabled)
	if err == nil {
		m.pruneLiveSuitesLocked(appID, name)
	}
	return view, err
}

func (m *MemStore) AdvanceAutomationLiveSuite(ctx context.Context, appID, name, id string, request api.AdvanceAutomationLiveSuiteRequest, runtimeEnabled bool) (api.AutomationLiveSuite, error) {
	if !request.ConfirmRealActions {
		return api.AutomationLiveSuite{}, api.ErrValidation("Confirm real actions before continuing this suite.")
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return api.AutomationLiveSuite{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.AutomationLiveSuite{}, err
	}
	record, ok := m.automationLiveSuites[parsed.String()]
	if !ok || record.AppID != appID || record.Name != name {
		return api.AutomationLiveSuite{}, ErrNotFound
	}
	if !record.Managed {
		return api.AutomationLiveSuite{}, ErrWorkflowRunIdempotencyConflict
	}
	return m.advanceManagedSuiteLocked(ctx, record, runtimeEnabled)
}

func (m *MemStore) advanceManagedSuiteLocked(ctx context.Context, record automationLiveSuiteRecord, runtimeEnabled bool) (api.AutomationLiveSuite, error) {
	current, ok := m.automations[record.AppID+"/"+record.Name]
	if !ok {
		return api.AutomationLiveSuite{}, ErrNotFound
	}
	record, err := advanceManagedSuite(record, current, m.automationDeploymentLocked(record.AppID).ID,
		func(id string) (api.AutomationLiveVerification, error) {
			return m.checkAutomationLiveVerificationLocked(ctx, record.AppID, record.Name, id)
		},
		func(key string, request api.StartAutomationLiveVerificationRequest) (api.AutomationLiveVerification, error) {
			view, _, err := m.startAutomationLiveVerificationLocked(ctx, record.AppID, record.Name, key, request, runtimeEnabled)
			return view, err
		})
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	if m.automationLiveSuites == nil {
		m.automationLiveSuites = map[string]automationLiveSuiteRecord{}
	}
	m.automationLiveSuites[record.ID] = record
	return m.liveSuiteViewLocked(record)
}
