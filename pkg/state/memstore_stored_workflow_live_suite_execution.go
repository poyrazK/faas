package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) StartStoredWorkflowLiveSuite(ctx context.Context, appID, name, id string, request api.StartStoredWorkflowLiveSuiteRequest, runtimeEnabled bool) (api.StoredWorkflowLiveSuite, error) {
	id, plan, err := managedSuitePlan(id, request)
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	if prior, ok := m.storedWorkflowLiveSuites[id]; ok {
		if prior.AppID != appID || prior.Name != name {
			return api.StoredWorkflowLiveSuite{}, ErrNotFound
		}
		if err := managedSuiteReplay(prior, plan); err != nil {
			return api.StoredWorkflowLiveSuite{}, err
		}
		return m.liveSuiteViewLocked(prior)
	}
	if err := validateManagedSuiteCreation(id); err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	if err := m.liveSuiteRoomLocked(appID, name); err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	current, ok := m.storedWorkflows[appID+"/"+name]
	if !ok {
		return api.StoredWorkflowLiveSuite{}, ErrNotFound
	}
	if err := validateLiveSuiteTarget(plan, current, m.storedWorkflowDeploymentLocked(appID).ID); err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	now := time.Now().UTC()
	record := storedWorkflowLiveSuiteRecord{ID: id, AppID: appID, Name: name, Plan: plan, Managed: true, CreatedAt: now, UpdatedAt: now}
	view, err := m.advanceManagedSuiteLocked(ctx, record, runtimeEnabled)
	if err == nil {
		m.pruneLiveSuitesLocked(appID, name)
	}
	return view, err
}

func (m *MemStore) AdvanceStoredWorkflowLiveSuite(ctx context.Context, appID, name, id string, request api.AdvanceStoredWorkflowLiveSuiteRequest, runtimeEnabled bool) (api.StoredWorkflowLiveSuite, error) {
	if !request.ConfirmRealActions {
		return api.StoredWorkflowLiveSuite{}, api.ErrValidation("Confirm real actions before continuing this suite.")
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	record, ok := m.storedWorkflowLiveSuites[parsed.String()]
	if !ok || record.AppID != appID || record.Name != name {
		return api.StoredWorkflowLiveSuite{}, ErrNotFound
	}
	if !record.Managed {
		return api.StoredWorkflowLiveSuite{}, ErrWorkflowRunIdempotencyConflict
	}
	return m.advanceManagedSuiteLocked(ctx, record, runtimeEnabled)
}

func (m *MemStore) advanceManagedSuiteLocked(ctx context.Context, record storedWorkflowLiveSuiteRecord, runtimeEnabled bool) (api.StoredWorkflowLiveSuite, error) {
	current, ok := m.storedWorkflows[record.AppID+"/"+record.Name]
	if !ok {
		return api.StoredWorkflowLiveSuite{}, ErrNotFound
	}
	record, err := advanceManagedSuite(record, current, m.storedWorkflowDeploymentLocked(record.AppID).ID,
		func(id string) (api.StoredWorkflowLiveVerification, error) {
			return m.checkStoredWorkflowLiveVerificationLocked(ctx, record.AppID, record.Name, id)
		},
		func(key string, request api.StartStoredWorkflowLiveVerificationRequest) (api.StoredWorkflowLiveVerification, error) {
			view, _, err := m.startStoredWorkflowLiveVerificationLocked(ctx, record.AppID, record.Name, key, request, runtimeEnabled)
			return view, err
		})
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	if m.storedWorkflowLiveSuites == nil {
		m.storedWorkflowLiveSuites = map[string]storedWorkflowLiveSuiteRecord{}
	}
	m.storedWorkflowLiveSuites[record.ID] = record
	return m.liveSuiteViewLocked(record)
}
