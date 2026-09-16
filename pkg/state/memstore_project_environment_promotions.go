package state

import (
	"context"
	"time"
)

func (m *MemStore) CreateProjectEnvironmentPromotion(_ context.Context, promotion ProjectEnvironmentPromotion, workloads []ProjectEnvironmentPromotionWorkload) (ProjectEnvironmentPromotion, []ProjectEnvironmentPromotionWorkload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.projectEnvironmentPromotions {
		if existing.AccountID == promotion.AccountID && existing.IdempotencyKey == promotion.IdempotencyKey {
			return ProjectEnvironmentPromotion{}, nil, ErrConflict
		}
	}
	if promotion.ID == "" {
		promotion.ID = newID()
	}
	now := time.Now().UTC()
	if promotion.Status == "" {
		promotion.Status = "running"
	}
	if promotion.VerificationStatus == "" {
		promotion.VerificationStatus = "pending"
	}
	if promotion.CreatedAt.IsZero() {
		promotion.CreatedAt = now
	}
	promotion.UpdatedAt = now
	m.projectEnvironmentPromotions[promotion.ID] = promotion
	items := make([]ProjectEnvironmentPromotionWorkload, len(workloads))
	for i, workload := range workloads {
		if workload.ID == "" {
			workload.ID = newID()
		}
		workload.PromotionID = promotion.ID
		if workload.Status == "" {
			workload.Status = "pending"
		}
		if workload.VerificationStatus == "" {
			workload.VerificationStatus = "pending"
		}
		if workload.CreatedAt.IsZero() {
			workload.CreatedAt = now
		}
		workload.UpdatedAt = now
		items[i] = workload
	}
	m.projectEnvironmentPromotionWorkloads[promotion.ID] = items
	return cloneProjectEnvironmentPromotion(promotion), cloneProjectEnvironmentPromotionWorkloads(items), nil
}

func (m *MemStore) UpdateProjectEnvironmentPromotionVerification(_ context.Context, accountID, id, status, errorMessage string, startedAt, completedAt *time.Time) (ProjectEnvironmentPromotion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	promotion, ok := m.projectEnvironmentPromotions[id]
	if !ok || promotion.AccountID != accountID {
		return ProjectEnvironmentPromotion{}, ErrNotFound
	}
	promotion.VerificationStatus = status
	promotion.VerificationError = errorMessage
	promotion.UpdatedAt = time.Now().UTC()
	if startedAt != nil {
		stamp := startedAt.UTC()
		promotion.VerificationStartedAt = &stamp
	}
	if completedAt != nil {
		stamp := completedAt.UTC()
		promotion.VerificationCompletedAt = &stamp
	}
	m.projectEnvironmentPromotions[id] = promotion
	return cloneProjectEnvironmentPromotion(promotion), nil
}

func (m *MemStore) UpdateProjectEnvironmentPromotionVerificationWorkload(_ context.Context, accountID, promotionID, workloadID, status, errorMessage string) (ProjectEnvironmentPromotionWorkload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	promotion, ok := m.projectEnvironmentPromotions[promotionID]
	if !ok || promotion.AccountID != accountID {
		return ProjectEnvironmentPromotionWorkload{}, ErrNotFound
	}
	items := m.projectEnvironmentPromotionWorkloads[promotionID]
	for i, workload := range items {
		if workload.ID != workloadID {
			continue
		}
		workload.VerificationStatus = status
		workload.VerificationError = errorMessage
		workload.UpdatedAt = time.Now().UTC()
		items[i] = workload
		m.projectEnvironmentPromotionWorkloads[promotionID] = items
		return workload, nil
	}
	return ProjectEnvironmentPromotionWorkload{}, ErrNotFound
}

func (m *MemStore) ProjectEnvironmentPromotionByID(_ context.Context, accountID, projectSlug, targetEnvironment, id string) (ProjectEnvironmentPromotion, []ProjectEnvironmentPromotionWorkload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	promotion, ok := m.projectEnvironmentPromotions[id]
	if !ok || promotion.AccountID != accountID || promotion.ProjectSlug != projectSlug || promotion.ToEnvironment != targetEnvironment {
		return ProjectEnvironmentPromotion{}, nil, ErrNotFound
	}
	return cloneProjectEnvironmentPromotion(promotion), cloneProjectEnvironmentPromotionWorkloads(m.projectEnvironmentPromotionWorkloads[id]), nil
}

func (m *MemStore) ProjectEnvironmentPromotionByIdempotencyKey(_ context.Context, accountID, projectSlug, idempotencyKey string) (ProjectEnvironmentPromotion, []ProjectEnvironmentPromotionWorkload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, promotion := range m.projectEnvironmentPromotions {
		if promotion.AccountID == accountID && promotion.ProjectSlug == projectSlug && promotion.IdempotencyKey == idempotencyKey {
			return cloneProjectEnvironmentPromotion(promotion), cloneProjectEnvironmentPromotionWorkloads(m.projectEnvironmentPromotionWorkloads[promotion.ID]), nil
		}
	}
	return ProjectEnvironmentPromotion{}, nil, ErrNotFound
}

func (m *MemStore) StartProjectEnvironmentPromotionRollback(_ context.Context, accountID, id, idempotencyKey string) (ProjectEnvironmentPromotion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	promotion, ok := m.projectEnvironmentPromotions[id]
	if !ok || promotion.AccountID != accountID {
		return ProjectEnvironmentPromotion{}, ErrNotFound
	}
	if promotion.RollbackIdempotencyKey != "" && promotion.RollbackIdempotencyKey != idempotencyKey {
		return ProjectEnvironmentPromotion{}, ErrConflict
	}
	if promotion.RollbackStatus == "rolled_back" {
		return cloneProjectEnvironmentPromotion(promotion), nil
	}
	now := time.Now().UTC()
	promotion.RollbackStatus = "rolling_back"
	promotion.RollbackIdempotencyKey = idempotencyKey
	promotion.RollbackError = ""
	if promotion.RollbackStartedAt == nil {
		promotion.RollbackStartedAt = &now
	}
	promotion.RollbackCompletedAt = nil
	promotion.UpdatedAt = now
	m.projectEnvironmentPromotions[id] = promotion
	return cloneProjectEnvironmentPromotion(promotion), nil
}

func (m *MemStore) UpdateProjectEnvironmentPromotionRollback(_ context.Context, accountID, id, status, errorMessage string, completedAt *time.Time) (ProjectEnvironmentPromotion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	promotion, ok := m.projectEnvironmentPromotions[id]
	if !ok || promotion.AccountID != accountID {
		return ProjectEnvironmentPromotion{}, ErrNotFound
	}
	promotion.RollbackStatus = status
	promotion.RollbackError = errorMessage
	promotion.UpdatedAt = time.Now().UTC()
	if completedAt != nil {
		stamp := completedAt.UTC()
		promotion.RollbackCompletedAt = &stamp
	}
	m.projectEnvironmentPromotions[id] = promotion
	return cloneProjectEnvironmentPromotion(promotion), nil
}

func (m *MemStore) UpdateProjectEnvironmentPromotionRollbackWorkload(_ context.Context, accountID, promotionID, workloadID, status, restoredTargetDeploymentID, errorMessage string) (ProjectEnvironmentPromotionWorkload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	promotion, ok := m.projectEnvironmentPromotions[promotionID]
	if !ok || promotion.AccountID != accountID {
		return ProjectEnvironmentPromotionWorkload{}, ErrNotFound
	}
	items := m.projectEnvironmentPromotionWorkloads[promotionID]
	for i, workload := range items {
		if workload.ID != workloadID {
			continue
		}
		workload.RollbackStatus = status
		workload.RestoredTargetDeploymentID = restoredTargetDeploymentID
		workload.RollbackError = errorMessage
		workload.UpdatedAt = time.Now().UTC()
		items[i] = workload
		m.projectEnvironmentPromotionWorkloads[promotionID] = items
		return workload, nil
	}
	return ProjectEnvironmentPromotionWorkload{}, ErrNotFound
}

func (m *MemStore) UpdateProjectEnvironmentPromotion(_ context.Context, accountID, id, status, errorMessage string, completedAt *time.Time) (ProjectEnvironmentPromotion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	promotion, ok := m.projectEnvironmentPromotions[id]
	if !ok || promotion.AccountID != accountID {
		return ProjectEnvironmentPromotion{}, ErrNotFound
	}
	promotion.Status = status
	promotion.Error = errorMessage
	promotion.UpdatedAt = time.Now().UTC()
	if completedAt != nil {
		stamp := completedAt.UTC()
		promotion.CompletedAt = &stamp
	}
	m.projectEnvironmentPromotions[id] = promotion
	return cloneProjectEnvironmentPromotion(promotion), nil
}

func (m *MemStore) UpdateProjectEnvironmentPromotionWorkload(_ context.Context, accountID, promotionID, workloadID, status, targetDeploymentID, errorMessage string) (ProjectEnvironmentPromotionWorkload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	promotion, ok := m.projectEnvironmentPromotions[promotionID]
	if !ok || promotion.AccountID != accountID {
		return ProjectEnvironmentPromotionWorkload{}, ErrNotFound
	}
	items := m.projectEnvironmentPromotionWorkloads[promotionID]
	for i, workload := range items {
		if workload.ID != workloadID {
			continue
		}
		workload.Status = status
		workload.TargetDeploymentID = targetDeploymentID
		workload.Error = errorMessage
		workload.UpdatedAt = time.Now().UTC()
		items[i] = workload
		m.projectEnvironmentPromotionWorkloads[promotionID] = items
		return workload, nil
	}
	return ProjectEnvironmentPromotionWorkload{}, ErrNotFound
}

func cloneProjectEnvironmentPromotion(promotion ProjectEnvironmentPromotion) ProjectEnvironmentPromotion {
	if promotion.CompletedAt != nil {
		stamp := *promotion.CompletedAt
		promotion.CompletedAt = &stamp
	}
	if promotion.RollbackStartedAt != nil {
		stamp := *promotion.RollbackStartedAt
		promotion.RollbackStartedAt = &stamp
	}
	if promotion.RollbackCompletedAt != nil {
		stamp := *promotion.RollbackCompletedAt
		promotion.RollbackCompletedAt = &stamp
	}
	if promotion.VerificationStartedAt != nil {
		stamp := *promotion.VerificationStartedAt
		promotion.VerificationStartedAt = &stamp
	}
	if promotion.VerificationCompletedAt != nil {
		stamp := *promotion.VerificationCompletedAt
		promotion.VerificationCompletedAt = &stamp
	}
	return promotion
}

func cloneProjectEnvironmentPromotionWorkloads(workloads []ProjectEnvironmentPromotionWorkload) []ProjectEnvironmentPromotionWorkload {
	return append([]ProjectEnvironmentPromotionWorkload(nil), workloads...)
}
