package state

import "errors"

type promotionWorkloadActivation struct {
	environmentID string
	appID         string
	specID        string
	app           App
	projectApp    bool
}

func (m *MemStore) promotionWorkloadActivationsLocked(promotion ProjectEnvironmentPromotion, members []ProjectReleaseMember, rollback, completed bool) ([]promotionWorkloadActivation, error) {
	var changes []promotionWorkloadActivation
	byApp := make(map[string]string, len(members))
	for _, member := range members {
		byApp[member.AppID] = member.DeploymentID
	}
	for _, capture := range m.projectEnvironmentPromotionWorkloadSpecs {
		if capture.PromotionID != promotion.ID {
			continue
		}
		app, ok := m.apps[capture.AppID]
		if !ok || app.AccountID != promotion.AccountID || app.ProjectID != promotion.ProjectID || app.Status == AppDeleted {
			return nil, ErrConflict
		}
		prepared, ok := m.projectEnvironmentWorkloadSpecs[capture.PreparedSpecID]
		if !ok || prepared.AppID != app.ID || prepared.EnvironmentSlug != promotion.ToEnvironment ||
			m.projectEnvironmentWorkloadDeploymentSpecs[capture.DeploymentID] != prepared.ID {
			return nil, ErrConflict
		}
		if !rollback && byApp[app.ID] != capture.DeploymentID {
			return nil, ErrConflict
		}
		current, settings, err := m.environmentWorkloadSettingsLocked(app, promotion.ToEnvironment)
		if err != nil {
			return nil, err
		}
		expectedID, expectedHash := capture.PreviousSpecID, capture.PreviousHash
		if rollback || completed {
			expectedID, expectedHash = prepared.ID, prepared.Hash
		}
		if rollback && completed {
			expectedID, expectedHash = capture.PreviousSpecID, capture.PreviousHash
		}
		hash, err := WorkloadSettingsHash(settings)
		if err != nil || current.ID != expectedID || hash != expectedHash {
			return nil, ErrConflict
		}
		targetID, targetSettings := prepared.ID, prepared.Settings
		if rollback {
			targetID, targetSettings = capture.PreviousSpecID, capture.PreviousSettings
		}
		targetHash, err := WorkloadSettingsHash(targetSettings)
		if err != nil || (rollback && targetHash != capture.PreviousHash) || (!rollback && targetHash != prepared.Hash) {
			return nil, ErrConflict
		}
		projected, err := targetSettings.ApplyTo(app)
		if err != nil {
			return nil, err
		}
		if hash != targetHash {
			projected.ScalingPolicyRevision++
		}
		changes = append(changes, promotionWorkloadActivation{environmentID: prepared.EnvironmentID,
			appID: app.ID, specID: targetID, app: projected, projectApp: promotion.ToEnvironment == "production"})
	}
	return changes, nil
}

func (m *MemStore) applyPromotionWorkloadActivationsLocked(changes []promotionWorkloadActivation) {
	for _, change := range changes {
		key := workloadSpecHeadKey(change.environmentID, change.appID)
		if change.specID == "" {
			delete(m.projectEnvironmentWorkloadHeads, key)
		} else {
			m.projectEnvironmentWorkloadHeads[key] = change.specID
		}
		if change.projectApp {
			m.apps[change.appID] = change.app
		}
	}
}

func (m *MemStore) syncProductionWorkloadSpecLocked(app App, params UpdateAppParams) (App, error) {
	if app.ProjectID == "" {
		return app, nil
	}
	env, err := m.workloadSpecEnvironmentLocked(app.AccountID, app.ProjectID, "production", app.ID)
	if errors.Is(err, ErrNotFound) {
		return app, nil
	}
	if err != nil {
		return App{}, err
	}
	current, found := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadHeads[workloadSpecHeadKey(env.ID, app.ID)]]
	if !found {
		return app, nil
	}
	prior, err := current.Settings.ApplyTo(app)
	if err != nil {
		return App{}, err
	}
	settings, err := WorkloadSettingsFromApp(applyAppConfigurationParams(prior, params))
	if err != nil {
		return App{}, err
	}
	hash, err := WorkloadSettingsHash(settings)
	if err != nil {
		return App{}, err
	}
	if _, err := m.putWorkloadSpecLocked(env, app.ID, current.Revision, settings, hash); err != nil {
		return App{}, err
	}
	return settings.ApplyTo(app)
}
