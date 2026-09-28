package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) PlanPlatformTenantReconciliation(ctx context.Context, in PlatformTenantReconciliationParams) (api.PlatformTenantReconciliationPlanResponse, error) {
	if err := validatePlatformTenantReconciliation(in); err != nil {
		return api.PlatformTenantReconciliationPlanResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot, err := m.planPlatformTenantReconciliationLocked(ctx, in)
	if err != nil {
		return api.PlatformTenantReconciliationPlanResponse{}, err
	}
	return api.PlatformTenantReconciliationPlanResponse{TenantID: in.TenantID,
		PlanHash: snapshot.planHash, Changes: snapshot.changes}, nil
}

func (m *MemStore) ApplyPlatformTenantReconciliation(ctx context.Context, in PlatformTenantReconciliationParams, expectedPlanHash string) (api.PlatformTenantReconciliationApplyResponse, error) {
	if !validPlatformTenantPlanHash(expectedPlanHash) {
		return api.PlatformTenantReconciliationApplyResponse{}, ErrInvalidArgument
	}
	if err := validatePlatformTenantReconciliation(in); err != nil {
		return api.PlatformTenantReconciliationApplyResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot, err := m.planPlatformTenantReconciliationLocked(ctx, in)
	if err != nil {
		return api.PlatformTenantReconciliationApplyResponse{}, err
	}
	if !platformTenantPlanHashMatches(expectedPlanHash, snapshot.planHash) {
		return api.PlatformTenantReconciliationApplyResponse{}, ErrPlatformTenantPlanStale
	}
	if err := m.validateManagedTenantRemovalsLocked(in.TenantID, snapshot.changes); err != nil {
		return api.PlatformTenantReconciliationApplyResponse{}, err
	}
	applyInput := in.ApplyPlatformTenantParams
	applyInput.DryRun = false
	result, err := m.applyPlatformTenantLocked(ctx, applyInput)
	if err != nil {
		return api.PlatformTenantReconciliationApplyResponse{}, err
	}
	if err := m.applyManagedTenantRemovalsLocked(in.TenantID, snapshot.changes); err != nil {
		return api.PlatformTenantReconciliationApplyResponse{}, err
	}
	return api.PlatformTenantReconciliationApplyResponse{TenantID: result.Tenant.ID, PlanHash: snapshot.planHash,
		Applied: true, Changes: platformTenantAppliedReconciliationChanges(snapshot.changes, result)}, nil
}

func (m *MemStore) planPlatformTenantReconciliationLocked(ctx context.Context, in PlatformTenantReconciliationParams) (platformTenantReconciliationSnapshot, error) {
	planInput := in.ApplyPlatformTenantParams
	planInput.DryRun = true
	planned, err := m.applyPlatformTenantLocked(ctx, planInput)
	if err != nil {
		return platformTenantReconciliationSnapshot{}, err
	}
	if planned.Tenant.ID == "" || planned.Tenant.ID != in.TenantID {
		return platformTenantReconciliationSnapshot{}, ErrNotFound
	}
	consumers := make([]APIConsumer, 0)
	for id, tenantID := range m.platformTenantByConsumer {
		if tenantID == in.TenantID {
			if consumer, ok := m.apiConsumers[id]; ok && consumer.AccountID == in.AccountID {
				consumers = append(consumers, consumer)
			}
		}
	}
	surfaces := make([]TenantSurface, 0)
	for id, tenantID := range m.platformTenantBySurface {
		if tenantID == in.TenantID {
			if surface, ok := m.tenantSurfaces[id]; ok && surface.AccountID == in.AccountID && surface.Status != SurfaceStatusDeleted {
				surfaces = append(surfaces, surface)
			}
		}
	}
	hostnamesBySurface := make(map[string][]TenantHostname)
	for i := len(planInput.SurfaceIDs); i < len(planned.Surfaces); i++ {
		surfaceID := planned.Surfaces[i].Surface.ID
		if surfaceID == "" {
			continue
		}
		for _, hostname := range m.tenantHostnames {
			if hostname.SurfaceID == surfaceID {
				hostnamesBySurface[surfaceID] = append(hostnamesBySurface[surfaceID], hostname)
			}
		}
	}
	changes := platformTenantReconciliationPlanChanges(planInput, planned, consumers, surfaces, hostnamesBySurface)
	return platformTenantReconciliationSnapshot{planned: planned, changes: changes,
		planHash: platformTenantReconciliationPlanHash(in.AccountID, in.TenantID, planInput, changes)}, nil
}

func (m *MemStore) validateManagedTenantRemovalsLocked(tenantID string, changes []api.PlatformTenantReconciliationPlanChange) error {
	for _, change := range changes {
		if change.Action != "remove_candidate" {
			continue
		}
		switch change.ResourceType {
		case "consumer":
			consumer, ok := m.apiConsumers[change.ID]
			if !ok || m.platformTenantByConsumer[change.ID] != tenantID || !consumer.PlatformTenantManaged {
				return ErrPlatformTenantPlanStale
			}
		case "surface":
			surface, ok := m.tenantSurfaces[change.ID]
			if !ok || m.platformTenantBySurface[change.ID] != tenantID || !surface.PlatformTenantManaged || surface.Status == SurfaceStatusDeleted {
				return ErrPlatformTenantPlanStale
			}
		case "hostname":
			found := false
			for _, hostname := range m.tenantHostnames {
				if hostname.ID == change.ID && hostname.SurfaceID == change.SurfaceID && hostname.PlatformTenantManaged {
					found = true
					break
				}
			}
			if !found || m.platformTenantBySurface[change.SurfaceID] != tenantID {
				return ErrPlatformTenantPlanStale
			}
		default:
			return ErrInvalidArgument
		}
	}
	return nil
}

func (m *MemStore) applyManagedTenantRemovalsLocked(tenantID string, changes []api.PlatformTenantReconciliationPlanChange) error {
	now := time.Now().UTC()
	for _, change := range changes {
		if change.Action != "remove_candidate" {
			continue
		}
		switch change.ResourceType {
		case "consumer":
			consumer := m.apiConsumers[change.ID]
			consumer.PlatformTenantID = ""
			consumer.UpdatedAt = now
			m.apiConsumers[change.ID] = consumer
			delete(m.platformTenantByConsumer, change.ID)
		case "surface":
			delete(m.platformTenantBySurface, change.ID)
		case "hostname":
			for key, hostname := range m.tenantHostnames {
				if hostname.ID == change.ID && hostname.SurfaceID == change.SurfaceID {
					delete(m.tenantHostnames, key)
				}
			}
		default:
			return ErrInvalidArgument
		}
	}
	return nil
}

var _ PlatformTenantReconciliationStore = (*MemStore)(nil)
