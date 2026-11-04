package state

import (
	"context"
	"time"
)

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

// A production promotion can change an app's visibility, which publishes or
// withdraws its hostnames (ADR-570). Analyze the projected apps before the
// release set or any setting is published.
func (m *MemStore) checkMemPromotionWorkloadBindingsLocked(ctx context.Context, accountID string, changes []promotionWorkloadActivation) error {
	staged := make(map[string]App)
	for _, change := range changes {
		if change.projectApp && m.apps[change.appID].Visibility != change.app.Visibility {
			staged[change.appID] = change.app
		}
	}
	if len(staged) == 0 {
		return nil
	}
	return appTrafficBindingError(m.checkMemTrafficBindingLocked(ctx, accountID, nil, "", memTrafficPolicyChange{Apps: staged}))
}

func (m *MemStore) applyPromotionWorkloadActivationsLocked(changes []promotionWorkloadActivation) {
	for _, change := range changes {
		key := workloadSpecHeadKey(change.environmentID, change.appID)
		if change.specID == "" {
			delete(m.projectEnvironmentWorkloadHeads, key)
		} else {
			m.putWorkloadSpecHeadLocked(m.projectEnvironmentWorkloadSpecs[change.specID])
		}
		if change.projectApp {
			m.apps[change.appID] = change.app
		}
	}
}

func (m *MemStore) syncProductionWorkloadSpecLocked(app App, params UpdateAppParams) (App, error) {
	updated, spec, err := m.prepareProductionWorkloadSpecLocked(app, params)
	if err == nil && spec.ID != "" {
		m.projectEnvironmentWorkloadSpecs[spec.ID] = spec
		m.putWorkloadSpecHeadLocked(spec)
	}
	return updated, err
}

// Preparation is pure so atomic project reconciliation can validate every app
// and cron before publishing any new workload settings heads.
func (m *MemStore) prepareProductionWorkloadSpecLocked(app App, params UpdateAppParams) (App, ProjectEnvironmentWorkloadSpec, error) {
	if app.ProjectID == "" {
		return app, ProjectEnvironmentWorkloadSpec{}, nil
	}
	var env ProjectEnvironment
	for _, candidate := range m.projectEnvironments {
		if candidate.AccountID == app.AccountID && candidate.ProjectID == app.ProjectID && candidate.Slug == "production" {
			env = candidate
			break
		}
	}
	current, found := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadHeads[workloadSpecHeadKey(env.ID, app.ID)]]
	if !found {
		return app, ProjectEnvironmentWorkloadSpec{}, nil
	}
	prior, err := current.Settings.ApplyTo(app)
	if err != nil {
		return App{}, ProjectEnvironmentWorkloadSpec{}, err
	}
	settings, err := WorkloadSettingsFromApp(applyAppConfigurationParams(prior, params))
	if err != nil {
		return App{}, ProjectEnvironmentWorkloadSpec{}, err
	}
	settings.WorkPolicies = current.Settings.WorkPolicies
	settings.QueueBindings = current.Settings.QueueBindings
	hash, err := WorkloadSettingsHash(settings)
	if err != nil {
		return App{}, ProjectEnvironmentWorkloadSpec{}, err
	}
	updated, err := settings.ApplyTo(app)
	if err != nil || hash == current.Hash {
		return updated, ProjectEnvironmentWorkloadSpec{}, err
	}
	return updated, ProjectEnvironmentWorkloadSpec{ID: newID(), AccountID: app.AccountID,
		ProjectID: app.ProjectID, EnvironmentID: env.ID, EnvironmentSlug: env.Slug, AppID: app.ID,
		Revision: m.nextWorkloadSpecRevisionLocked(env.ID, app.ID), Hash: hash, Settings: settings, CreatedAt: time.Now().UTC()}, nil
}
