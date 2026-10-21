package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"sort"
	"time"
)

func (m *MemStore) CreateProjectEnvironmentPromotion(ctx context.Context, promotion ProjectEnvironmentPromotion, workloads []ProjectEnvironmentPromotionWorkload) (ProjectEnvironmentPromotion, []ProjectEnvironmentPromotionWorkload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return ProjectEnvironmentPromotion{}, nil, err
	}
	for _, existing := range m.projectEnvironmentPromotions {
		if existing.AccountID == promotion.AccountID && existing.IdempotencyKey == promotion.IdempotencyKey {
			return ProjectEnvironmentPromotion{}, nil, ErrConflict
		}
	}
	if promotion.ID == "" {
		promotion.ID = newID()
	}
	now := time.Now().UTC()
	health, err := newPromotionHealth(promotion, m.clock())
	if err != nil {
		return ProjectEnvironmentPromotion{}, nil, err
	}
	if err := admitPromotionHealth(health, m.promotionAdmissionCountsLocked(promotion.AccountID, promotion.ID), m.clock().UTC()); err != nil {
		return ProjectEnvironmentPromotion{}, nil, err
	}
	approval, err := m.findPromotionApprovalLocked(promotion)
	if err != nil {
		return ProjectEnvironmentPromotion{}, nil, err
	}
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
	var releaseCapture *promotionReleaseConfiguration
	if promotion.SyncConfig {
		var err error
		releaseCapture, err = m.capturePromotionReleaseConfigurationLocked(promotion, workloads)
		if err != nil {
			return ProjectEnvironmentPromotion{}, nil, err
		}
		if err := m.admitPromotionMigrationLocked(promotion, releaseCapture); err != nil {
			return ProjectEnvironmentPromotion{}, nil, err
		}
		captured, err := m.capturePromotionFeatureFlagsLocked(promotion)
		if err != nil {
			return ProjectEnvironmentPromotion{}, nil, err
		}
		if m.projectEnvironmentPromotionFlags == nil {
			m.projectEnvironmentPromotionFlags = map[string]promotionFeatureFlags{}
		}
		m.projectEnvironmentPromotionFlags[promotion.ID] = captured
	}
	if releaseCapture != nil {
		if m.promotionReleaseConfigurations == nil {
			m.promotionReleaseConfigurations = map[string]promotionReleaseConfiguration{}
		}
		if m.releaseEnvironmentPins == nil {
			m.releaseEnvironmentPins = map[string][]AppEnv{}
		}
		m.promotionReleaseConfigurations[promotion.ID] = *releaseCapture
		for _, w := range releaseCapture.Workloads {
			if releaseCapture.Secrets != nil {
				if m.releaseSecretPins == nil {
					m.releaseSecretPins = map[string][]releaseSecretVersion{}
				}
				m.releaseSecretPins[w.Previous.ID] = cloneReleaseSecrets(*w.SecretSnapshot)
			}
			m.releaseEnvironmentPins[w.Previous.ID] = releaseVariableRows(w.PreviousVariables)
		}
	}
	if releaseCapture != nil && releaseCapture.Migration != nil {
		m.promotionMigrations[promotion.ID] = promotionMigrationCapture{}
	}
	if health != nil {
		if m.promotionHealth == nil {
			m.promotionHealth = map[string]*api.ProjectPromotionHealth{}
		}
		m.promotionHealth[promotion.ID] = health
	}
	promotion.ReleaseSecrets = nil
	promotion.HealthPolicy = nil
	promotion.ResolveServiceBindings = false
	promotion.Migrate = false
	promotion.Workers = false
	if approval != nil {
		at := m.clock().UTC()
		approval.ConsumedAt = &at
		m.projectEnvironmentApprovals[approval.ID] = *approval
		promotion.ConsumedApproval = approval
	}
	stored := promotion
	stored.Approval, stored.ConsumedApproval = nil, nil
	m.projectEnvironmentPromotions[promotion.ID] = stored
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

func (m *MemStore) UpdateProjectEnvironmentPromotionVerification(ctx context.Context, accountID, id, status, errorMessage string, startedAt, completedAt *time.Time) (ProjectEnvironmentPromotion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return ProjectEnvironmentPromotion{}, err
	}
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

func (m *MemStore) UpdateProjectEnvironmentPromotionVerificationWorkload(ctx context.Context, accountID, promotionID, workloadID, status, errorMessage string) (ProjectEnvironmentPromotionWorkload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return ProjectEnvironmentPromotionWorkload{}, err
	}
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

func (m *MemStore) UpdateProjectEnvironmentPromotionReleaseSets(ctx context.Context, accountID, id, targetReleaseSetID, restoredTargetReleaseSetID string) (ProjectEnvironmentPromotion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return ProjectEnvironmentPromotion{}, err
	}
	promotion, ok := m.projectEnvironmentPromotions[id]
	if !ok || promotion.AccountID != accountID {
		return ProjectEnvironmentPromotion{}, ErrNotFound
	}
	if h := m.promotionHealth[id]; h != nil && h.Control != nil {
		if targetReleaseSetID != "" && targetReleaseSetID != promotion.TargetReleaseSetID || restoredTargetReleaseSetID != "" && restoredTargetReleaseSetID != promotion.RestoredTargetReleaseSetID {
			return ProjectEnvironmentPromotion{}, ErrConflict
		}
	}
	if targetReleaseSetID != "" {
		promotion.TargetReleaseSetID = targetReleaseSetID
	}
	if restoredTargetReleaseSetID != "" {
		promotion.RestoredTargetReleaseSetID = restoredTargetReleaseSetID
	}
	promotion.UpdatedAt = time.Now().UTC()
	m.projectEnvironmentPromotions[id] = promotion
	return cloneProjectEnvironmentPromotion(promotion), nil
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

// ListProjectEnvironmentPromotionsBefore returns newest-first promotion
// history for one target environment. The compound (created_at, id) cursor
// keeps equal-timestamp rows stable across pages and mirrors PgStore.
func (m *MemStore) ListProjectEnvironmentPromotionsBefore(_ context.Context, accountID, projectSlug, targetEnvironment, sourceEnvironment, status string, before time.Time, beforeID string, limit int) ([]ProjectEnvironmentPromotion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]ProjectEnvironmentPromotion, 0)
	for _, promotion := range m.projectEnvironmentPromotions {
		if promotion.AccountID != accountID || promotion.ProjectSlug != projectSlug || promotion.ToEnvironment != targetEnvironment {
			continue
		}
		if sourceEnvironment != "" && promotion.FromEnvironment != sourceEnvironment {
			continue
		}
		if status != "" && promotion.Status != status {
			continue
		}
		if !before.IsZero() {
			if promotion.CreatedAt.After(before) || (promotion.CreatedAt.Equal(before) && (beforeID == "" || promotion.ID >= beforeID)) {
				continue
			}
		}
		items = append(items, cloneProjectEnvironmentPromotion(promotion))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (m *MemStore) StartProjectEnvironmentPromotionRollback(ctx context.Context, accountID, id, idempotencyKey string) (ProjectEnvironmentPromotion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return ProjectEnvironmentPromotion{}, err
	}
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

func (m *MemStore) UpdateProjectEnvironmentPromotionRollback(ctx context.Context, accountID, id, status, errorMessage string, completedAt *time.Time) (ProjectEnvironmentPromotion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return ProjectEnvironmentPromotion{}, err
	}
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

func (m *MemStore) UpdateProjectEnvironmentPromotionRollbackWorkload(ctx context.Context, accountID, promotionID, workloadID, status, restoredTargetDeploymentID, errorMessage string) (ProjectEnvironmentPromotionWorkload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return ProjectEnvironmentPromotionWorkload{}, err
	}
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

func (m *MemStore) UpdateProjectEnvironmentPromotion(ctx context.Context, accountID, id, status, errorMessage string, completedAt *time.Time) (ProjectEnvironmentPromotion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return ProjectEnvironmentPromotion{}, err
	}
	promotion, ok := m.projectEnvironmentPromotions[id]
	if !ok || promotion.AccountID != accountID {
		return ProjectEnvironmentPromotion{}, ErrNotFound
	}
	if h := m.promotionHealth[id]; h != nil {
		if status == "running" && promotion.Status != "running" || status == "succeeded" && h.Phase != "succeeded" || status == "failed" && h.Phase != "preflight" && h.Phase != "failed" && h.Phase != "conflicted" && h.Phase != "rollback_failed" {
			return ProjectEnvironmentPromotion{}, ErrConflict
		}
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

func (m *MemStore) UpdateProjectEnvironmentPromotionWorkload(ctx context.Context, accountID, promotionID, workloadID, status, targetDeploymentID, errorMessage string) (ProjectEnvironmentPromotionWorkload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return ProjectEnvironmentPromotionWorkload{}, err
	}
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
	promotion.SourceConfigSnapshot = append([]byte(nil), promotion.SourceConfigSnapshot...)
	promotion.PreviousTargetConfigSnapshot = append([]byte(nil), promotion.PreviousTargetConfigSnapshot...)
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
