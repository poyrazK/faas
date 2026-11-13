// adr: 20261104104521
package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) scalingAppLocked(accountID, appID, scope string) (App, ProjectEnvironmentWorkloadSpec, error) {
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return App{}, ProjectEnvironmentWorkloadSpec{}, ErrNotFound
	}
	if app.ScalingRevision < 1 {
		app.ScalingRevision = 1
	}
	if scope == "default" {
		return app, ProjectEnvironmentWorkloadSpec{}, nil
	}
	env, err := m.workloadSpecEnvironmentLocked(accountID, app.ProjectID, scope, appID)
	if err != nil {
		return App{}, ProjectEnvironmentWorkloadSpec{}, err
	}
	spec, ok := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadHeads[workloadSpecHeadKey(env.ID, appID)]]
	if !ok {
		return App{}, ProjectEnvironmentWorkloadSpec{}, ErrNotFound
	}
	app, err = spec.Settings.ApplyTo(app)
	return app, spec, err
}

func (m *MemStore) AppScaling(ctx context.Context, accountID, appID, scope string) (api.AppScalingSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return api.AppScalingSnapshot{}, err
	}
	app, spec, err := m.scalingAppLocked(accountID, appID, scope)
	if err != nil {
		return api.AppScalingSnapshot{}, err
	}
	revision := app.ScalingRevision
	if scope != "default" {
		revision = spec.Revision
	}
	return api.AppScalingSnapshot{AppID: appID, Scope: scope, EnvironmentID: spec.EnvironmentID, Revision: revision, Settings: scalingSettings(app)}, nil
}

func (m *MemStore) AppScalingReceipt(ctx context.Context, accountID, appID, scope, requestID string) (api.AppScalingReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return api.AppScalingReceipt{}, err
	}
	environmentID := ""
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return api.AppScalingReceipt{}, ErrNotFound
	}
	if scope != "default" {
		env, err := m.workloadSpecEnvironmentLocked(accountID, app.ProjectID, scope, appID)
		if err != nil {
			return api.AppScalingReceipt{}, err
		}
		environmentID = env.ID
	}
	record, ok := m.scalingReceipts[appID+":"+scope+":"+requestID]
	if !ok || record.Receipt.EnvironmentID != environmentID {
		return api.AppScalingReceipt{}, ErrNotFound
	}
	return record.Receipt, nil
}

func (m *MemStore) ApplyAppScaling(ctx context.Context, in AppScalingChange) (api.AppScalingReceipt, error) {
	hash, err := validateScalingChange(&in)
	if err != nil {
		return api.AppScalingReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return api.AppScalingReceipt{}, err
	}
	app, ok := m.apps[in.AppID]
	if !ok || app.AccountID != in.AccountID || app.Status == AppDeleted {
		return api.AppScalingReceipt{}, ErrNotFound
	}
	var spec ProjectEnvironmentWorkloadSpec
	var environment ProjectEnvironment
	revision := max(app.ScalingRevision, 1)
	if in.Scope != "default" {
		environment, err = m.workloadSpecEnvironmentLocked(in.AccountID, app.ProjectID, in.Scope, app.ID)
		if err != nil {
			return api.AppScalingReceipt{}, err
		}
		if !scalingIdentityMatches(environment.ID, in.Request.ExpectedEnvironmentID) {
			return api.AppScalingReceipt{}, ErrConflict
		}
		if environment.Protected {
			return api.AppScalingReceipt{}, ErrConflict
		}
	}
	key := app.ID + ":" + in.Scope + ":" + in.Request.RequestID
	if previous, ok := m.scalingReceipts[key]; ok {
		if previous.Hash != hash {
			return api.AppScalingReceipt{}, ErrScalingRequestConflict
		}
		return previous.Receipt, nil
	}
	if in.Scope != "default" {
		app, spec, err = m.scalingAppLocked(in.AccountID, app.ID, in.Scope)
		if err != nil {
			return api.AppScalingReceipt{}, err
		}
		revision = spec.Revision
	}
	if revision != *in.Request.ExpectedRevision {
		return api.AppScalingReceipt{}, ErrScalingRevisionConflict
	}
	if in.Validate != nil {
		if err := in.Validate(app); err != nil {
			return api.AppScalingReceipt{}, err
		}
	}
	params := reviewedScalingParams(app, in.Request)
	var nextRevision int64
	if in.Scope == "default" {
		app, err = m.updateAppLocked(ctx, app.ID, params, nil, nil, nil)
		if err == nil {
			if app.ScalingRevision <= revision {
				app.ScalingRevision = revision + 1
			}
			m.apps[app.ID] = app
			nextRevision = app.ScalingRevision
		}
	} else {
		var settings ProjectEnvironmentWorkloadSettings
		settings, err = ApplyWorkloadSettingsUpdate(spec.Settings, params)
		if err == nil {
			var settingsHash string
			settingsHash, err = WorkloadSettingsHash(settings)
			if err == nil {
				spec, err = m.putWorkloadSpecLocked(m.projectEnvironments[spec.EnvironmentID], app.ID, revision, settings, settingsHash)
			}
		}
		if err == nil {
			app, err = spec.Settings.ApplyTo(app)
			nextRevision = spec.Revision
		}
	}
	if err != nil {
		return api.AppScalingReceipt{}, err
	}
	out := api.AppScalingReceipt{AppScalingSnapshot: api.AppScalingSnapshot{AppID: app.ID, Scope: in.Scope, EnvironmentID: spec.EnvironmentID, Revision: nextRevision, Settings: scalingSettings(app)}, RequestID: in.Request.RequestID, PreviousRevision: revision, CommittedAt: time.Now().UTC()}
	if m.scalingReceipts == nil {
		m.scalingReceipts = map[string]scalingRecord{}
	}
	m.scalingReceipts[key] = scalingRecord{Hash: hash, Receipt: out}
	return out, nil
}
