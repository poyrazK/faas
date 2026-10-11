package state

import (
	"context"
	"time"
)

func (m *MemStore) CreateDeploymentForEnvironmentPromotion(_ context.Context, deployment Deployment, input ProjectEnvironmentPromotionWorkloadSpecInput) (Deployment, error) {
	created, _, err := m.createDeployment(deployment, nil, &input)
	return created, err
}

func (m *MemStore) ProjectEnvironmentPromotionWorkloadSpec(_ context.Context, accountID, promotionID, appID string) (ProjectEnvironmentPromotionWorkloadSpec, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	promotion, ok := m.projectEnvironmentPromotions[promotionID]
	capture, found := m.projectEnvironmentPromotionWorkloadSpecs[workloadSpecHeadKey(promotionID, appID)]
	if !ok || !found || promotion.AccountID != accountID {
		return ProjectEnvironmentPromotionWorkloadSpec{}, ErrNotFound
	}
	return clonePromotionWorkloadSpec(capture)
}

func (m *MemStore) environmentWorkloadSettingsLocked(app App, environment string) (ProjectEnvironmentWorkloadSpec, ProjectEnvironmentWorkloadSettings, error) {
	env, err := m.workloadSpecEnvironmentLocked(app.AccountID, app.ProjectID, environment, app.ID)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, ProjectEnvironmentWorkloadSettings{}, err
	}
	if spec, found := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadHeads[workloadSpecHeadKey(env.ID, app.ID)]]; found {
		settings, err := cloneWorkloadSettings(spec.Settings)
		hash, hashErr := WorkloadSettingsHash(settings)
		if err != nil || hashErr != nil || hash != spec.Hash {
			return ProjectEnvironmentWorkloadSpec{}, ProjectEnvironmentWorkloadSettings{}, ErrConflict
		}
		return spec, settings, nil
	}
	settings, err := WorkloadSettingsFromApp(app)
	if route, ok := m.projectEnvironmentRoutePolicies[projectEnvironmentRoutePolicyKey(app.ID, environment)]; ok {
		settings.OnlyAllowDeclaredRoutes, settings.DeclaredRoutes = route.OnlyAllowDeclaredRoutes, cloneDeclaredRoutes(route.DeclaredRoutes)
	}
	return ProjectEnvironmentWorkloadSpec{}, settings, err
}

func (m *MemStore) nextWorkloadSpecRevisionLocked(environmentID, appID string) int64 {
	var revision int64
	for _, spec := range m.projectEnvironmentWorkloadSpecs {
		if spec.EnvironmentID == environmentID && spec.AppID == appID && spec.Revision > revision {
			revision = spec.Revision
		}
	}
	return revision + 1
}

func (m *MemStore) preparePromotionWorkloadSpecLocked(app App, environment string, input ProjectEnvironmentPromotionWorkloadSpecInput) (ProjectEnvironmentWorkloadSpec, ProjectEnvironmentPromotionWorkloadSpec, error) {
	invalid := func(err error) (ProjectEnvironmentWorkloadSpec, ProjectEnvironmentPromotionWorkloadSpec, error) {
		return ProjectEnvironmentWorkloadSpec{}, ProjectEnvironmentPromotionWorkloadSpec{}, err
	}
	promotion, ok := m.projectEnvironmentPromotions[input.PromotionID]
	if !ok || promotion.AccountID != app.AccountID || promotion.ProjectID != app.ProjectID || promotion.ToEnvironment != environment ||
		!promotion.SyncConfig || !promotion.ReleaseGraphMode || promotion.Status != "running" || promotion.TargetReleaseSetID != "" {
		return invalid(ErrConflict)
	}
	matched := false
	var previousDeploymentID string
	for _, workload := range m.projectEnvironmentPromotionWorkloads[promotion.ID] {
		if workload.WorkloadSlug == app.Slug && workload.SourceDeploymentID == input.SourceDeploymentID {
			matched = true
			previousDeploymentID = workload.PreviousTargetDeploymentID
		}
	}
	source, exists := m.deployments[input.SourceDeploymentID]
	if !matched || !exists || source.AppID != app.ID || source.Scope != promotion.FromEnvironment || source.Status != DeployLive {
		return invalid(ErrConflict)
	}
	if _, found := m.projectEnvironmentPromotionWorkloadSpecs[workloadSpecHeadKey(promotion.ID, app.ID)]; found {
		return invalid(ErrConflict)
	}
	env, err := m.workloadSpecEnvironmentLocked(app.AccountID, app.ProjectID, environment, app.ID)
	if err != nil {
		return invalid(err)
	}
	previous, targetSettings, err := m.environmentWorkloadSettingsLocked(app, environment)
	if err != nil {
		return invalid(err)
	}
	sourceSettings, err := WorkloadSettingsFromApp(app)
	if err != nil {
		return invalid(err)
	}
	if route, ok := m.projectEnvironmentRoutePolicies[projectEnvironmentRoutePolicyKey(app.ID, promotion.FromEnvironment)]; ok {
		sourceSettings.OnlyAllowDeclaredRoutes, sourceSettings.DeclaredRoutes = route.OnlyAllowDeclaredRoutes, cloneDeclaredRoutes(route.DeclaredRoutes)
	}
	if pinned, found := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadDeploymentSpecs[source.ID]]; found {
		if pinned.AppID != app.ID || pinned.EnvironmentSlug != promotion.FromEnvironment {
			return invalid(ErrConflict)
		}
		sourceSettings = pinned.Settings
	}
	sourceHash, err := WorkloadSettingsHash(sourceSettings)
	if err != nil || sourceHash != input.SourceHash {
		return invalid(ErrConflict)
	}
	targetHash, err := WorkloadSettingsHash(targetSettings)
	if err != nil || targetHash != input.PreviousTargetHash {
		return invalid(ErrConflict)
	}
	settings, err := WorkloadSettingsForPromotion(sourceSettings, targetSettings)
	if err != nil {
		return invalid(err)
	}
	hash, err := WorkloadSettingsHash(settings)
	if err != nil {
		return invalid(err)
	}
	spec := ProjectEnvironmentWorkloadSpec{
		ID: newID(), AccountID: app.AccountID, ProjectID: app.ProjectID, EnvironmentID: env.ID,
		EnvironmentSlug: env.Slug, AppID: app.ID, Revision: m.nextWorkloadSpecRevisionLocked(env.ID, app.ID),
		Hash: hash, Settings: settings, CreatedAt: time.Now().UTC(),
	}
	capture := ProjectEnvironmentPromotionWorkloadSpec{
		PromotionID: promotion.ID, AppID: app.ID, PreparedSpecID: spec.ID, PreviousSpecID: previous.ID,
		SourceHash: sourceHash, PreviousHash: targetHash, PreviousSettings: targetSettings,
	}
	// Freeze an existing legacy target before projecting promoted settings onto
	// App. Retained clients must still receive its original configuration.
	if previousDeploymentID != "" && m.projectEnvironmentWorkloadDeploymentSpecs[previousDeploymentID] == "" {
		legacy, err := WorkloadSettingsFromApp(app)
		if err != nil {
			return invalid(err)
		}
		if route, ok := m.projectEnvironmentRoutePolicies[projectEnvironmentRoutePolicyKey(app.ID, environment)]; ok {
			legacy.OnlyAllowDeclaredRoutes, legacy.DeclaredRoutes = route.OnlyAllowDeclaredRoutes, cloneDeclaredRoutes(route.DeclaredRoutes)
		}
		legacyHash, err := WorkloadSettingsHash(legacy)
		if err != nil {
			return invalid(err)
		}
		capture.legacyPreviousDeploymentID = previousDeploymentID
		capture.legacyPreviousSpec = spec
		capture.legacyPreviousSpec.ID = newID()
		capture.legacyPreviousSpec.Hash, capture.legacyPreviousSpec.Settings = legacyHash, legacy
		spec.Revision++
	}
	return spec, capture, nil
}
