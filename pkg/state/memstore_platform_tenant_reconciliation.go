package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
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
	changes := platformTenantAppliedReconciliationChanges(snapshot.changes, result)
	response := api.PlatformTenantReconciliationApplyResponse{TenantID: result.Tenant.ID, ReceiptID: uuid.NewString(),
		PlanHash: snapshot.planHash, AppliedAt: m.clock().UTC(), Applied: true, Changes: changes}
	receipt := api.PlatformTenantReconciliationReceiptResponse{
		TenantID: response.TenantID, ReceiptID: response.ReceiptID, PlanHash: response.PlanHash,
		AppliedAt: response.AppliedAt, Changes: append([]api.PlatformTenantReconciliationPlanChange(nil), changes...),
	}
	m.platformTenantReconciliationReceipts[response.ReceiptID] = receipt
	m.enqueuePlatformTenantReconciliationAppliedWebhooksLocked(receipt)
	return response, nil
}

func (m *MemStore) ListPlatformTenantReconciliationReceipts(_ context.Context, accountID, tenantID string, pageSize int, pageToken string) ([]api.PlatformTenantReconciliationReceiptSummary, string, error) {
	if pageSize < 1 || pageSize > 100 {
		return nil, "", ErrInvalidArgument
	}
	var tokenTime time.Time
	var tokenID string
	if pageToken != "" {
		var valid bool
		tokenTime, tokenID, valid = decodePageToken(pageToken)
		if _, err := uuid.Parse(tokenID); !valid || err != nil {
			return nil, "", ErrInvalidArgument
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return nil, "", ErrNotFound
	}
	rows := make([]api.PlatformTenantReconciliationReceiptResponse, 0)
	for _, receipt := range m.platformTenantReconciliationReceipts {
		if receipt.TenantID == tenantID {
			rows = append(rows, receipt)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].AppliedAt.Equal(rows[j].AppliedAt) {
			return rows[i].ReceiptID > rows[j].ReceiptID
		}
		return rows[i].AppliedAt.After(rows[j].AppliedAt)
	})
	if pageToken != "" {
		filtered := rows[:0]
		for _, receipt := range rows {
			if receipt.AppliedAt.Before(tokenTime) || (receipt.AppliedAt.Equal(tokenTime) && receipt.ReceiptID < tokenID) {
				filtered = append(filtered, receipt)
			}
		}
		rows = filtered
	}
	var nextToken string
	if len(rows) > pageSize {
		last := rows[pageSize-1]
		nextToken = encodePageToken(last.AppliedAt, last.ReceiptID)
		rows = rows[:pageSize]
	}
	out := make([]api.PlatformTenantReconciliationReceiptSummary, 0, len(rows))
	for _, receipt := range rows {
		out = append(out, api.PlatformTenantReconciliationReceiptSummary{ReceiptID: receipt.ReceiptID,
			PlanHash: receipt.PlanHash, AppliedAt: receipt.AppliedAt, ChangeCount: len(receipt.Changes)})
	}
	return out, nextToken, nil
}

func (m *MemStore) GetPlatformTenantReconciliationReceipt(_ context.Context, accountID, tenantID, receiptID string) (api.PlatformTenantReconciliationReceiptResponse, error) {
	if _, err := uuid.Parse(receiptID); err != nil {
		return api.PlatformTenantReconciliationReceiptResponse{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return api.PlatformTenantReconciliationReceiptResponse{}, ErrNotFound
	}
	receipt, ok := m.platformTenantReconciliationReceipts[receiptID]
	if !ok || receipt.TenantID != tenantID {
		return api.PlatformTenantReconciliationReceiptResponse{}, ErrNotFound
	}
	receipt.Changes = append([]api.PlatformTenantReconciliationPlanChange(nil), receipt.Changes...)
	return receipt, nil
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
