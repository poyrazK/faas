package state

import "context"

func (m *MemStore) promotionFeatureFlagHeadLocked(promotion ProjectEnvironmentPromotion, environment string) (FeatureFlagVersion, error) {
	project, ok := m.projects[promotion.ProjectID]
	if !ok || project.AccountID != promotion.AccountID {
		return FeatureFlagVersion{}, ErrNotFound
	}
	env, err := m.projectEnvironmentBySlugLocked(promotion.ProjectID, environment)
	if err != nil || env.AccountID != promotion.AccountID {
		return FeatureFlagVersion{}, ErrNotFound
	}
	return m.latestFeatureFlagsLocked(FeatureFlagScope{AccountID: promotion.AccountID, ProjectID: promotion.ProjectID, EnvironmentID: env.ID}), nil
}

func (m *MemStore) capturePromotionFeatureFlagsLocked(promotion ProjectEnvironmentPromotion) (promotionFeatureFlags, error) {
	source, err := m.promotionFeatureFlagHeadLocked(promotion, promotion.FromEnvironment)
	if err != nil {
		return promotionFeatureFlags{}, err
	}
	target, err := m.promotionFeatureFlagHeadLocked(promotion, promotion.ToEnvironment)
	if err != nil {
		return promotionFeatureFlags{}, err
	}
	return capturePromotionFeatureFlags(promotion, source, target)
}

func (m *MemStore) ProjectEnvironmentPromotionFeatureFlags(_ context.Context, accountID, promotionID string) (ProjectEnvironmentPromotionFeatureFlags, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	promotion, ok := m.projectEnvironmentPromotions[promotionID]
	if !ok || promotion.AccountID != accountID {
		return ProjectEnvironmentPromotionFeatureFlags{}, ErrNotFound
	}
	captured, ok := m.projectEnvironmentPromotionFlags[promotionID]
	if !ok {
		return ProjectEnvironmentPromotionFeatureFlags{}, ErrNotFound
	}
	if err := captured.authenticate(); err != nil {
		return ProjectEnvironmentPromotionFeatureFlags{}, err
	}
	return captured.ProjectEnvironmentPromotionFeatureFlags, nil
}

func (m *MemStore) preparePromotionFeatureFlagsLocked(promotion ProjectEnvironmentPromotion, rollback, completed bool) (FeatureFlagVersion, error) {
	if !promotion.SyncConfig {
		return FeatureFlagVersion{}, nil
	}
	captured, ok := m.projectEnvironmentPromotionFlags[promotion.ID]
	if !ok {
		return FeatureFlagVersion{}, ErrConflict
	}
	target, err := m.promotionFeatureFlagHeadLocked(promotion, promotion.ToEnvironment)
	if err != nil {
		return FeatureFlagVersion{}, err
	}
	var source FeatureFlagVersion
	if !rollback && !completed {
		source, err = m.promotionFeatureFlagHeadLocked(promotion, promotion.FromEnvironment)
		if err != nil {
			return FeatureFlagVersion{}, err
		}
	}
	next, err := preparePromotionFeatureFlagActivation(captured, source, target, rollback, completed)
	if err != nil {
		return FeatureFlagVersion{}, err
	}
	for _, id := range flagCustomerIDs(next.Config) {
		tenant, ok := m.platformTenants[id]
		if !ok || tenant.AccountID != promotion.AccountID {
			return FeatureFlagVersion{}, ErrConflict
		}
	}
	return next, nil
}

func (m *MemStore) applyPromotionFeatureFlagsLocked(promotion ProjectEnvironmentPromotion, next FeatureFlagVersion, rollback bool) {
	if !promotion.SyncConfig {
		return
	}
	if m.featureFlagVersions == nil {
		m.featureFlagVersions = map[string][]FeatureFlagVersion{}
	}
	m.featureFlagVersions[next.EnvironmentID] = append(m.featureFlagVersions[next.EnvironmentID], next)
	captured := m.projectEnvironmentPromotionFlags[promotion.ID]
	if rollback {
		captured.RollbackVersion = next.Version
	} else {
		captured.TargetVersion = next.Version
	}
	m.projectEnvironmentPromotionFlags[promotion.ID] = captured
}
