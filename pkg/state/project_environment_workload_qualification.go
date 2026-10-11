package state

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

// ProjectEnvironmentWorkloadQualificationStore fingerprints runtime settings
// and the observed flag revision for every active release member, rejecting
// undeployed desired edits and flags changed since the qualification probes.
type ProjectEnvironmentWorkloadQualificationStore interface {
	ProjectEnvironmentWorkloadConfigHashes(context.Context, string, string, string, string) (map[string]string, bool, error)
}

func qualificationWorkloadHash(desired, pinned ProjectEnvironmentWorkloadSpec, legacy ProjectEnvironmentWorkloadSettings) (string, error) {
	if desired.Settings.QueueBindings != nil || pinned.Settings.QueueBindings != nil {
		return "", ErrProjectEnvironmentQueueActivationUnavailable
	}
	if desired.Settings.WorkPolicies != nil || pinned.Settings.WorkPolicies != nil {
		return "", ErrProjectEnvironmentWorkPolicyActivationUnavailable
	}
	hash, err := WorkloadSettingsHash(legacy)
	if err != nil {
		return "", err
	}
	if desired.ID != "" {
		hash, err = WorkloadSettingsHash(desired.Settings)
		if err != nil || hash != desired.Hash || pinned.ID == "" {
			return "", ErrConflict
		}
	}
	if pinned.ID != "" {
		pinnedHash, err := WorkloadSettingsHash(pinned.Settings)
		if err != nil || pinnedHash != pinned.Hash || pinnedHash != hash {
			return "", ErrConflict
		}
	}
	return hash, nil
}

func validateQualificationWorkloadHashes(expected, actual map[string]string, scoped bool) error {
	// Legacy receipts remain usable only while no environment-owned settings
	// or published flag revision exists. New clients send the identities
	// observed before their probes.
	if len(expected) == 0 && !scoped {
		return nil
	}
	if !sameProjectEnvironmentSecretRevisionHashes(expected, actual) {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) ProjectEnvironmentWorkloadConfigHashes(_ context.Context, accountID, projectID, environment, releaseSetID string) (map[string]string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ownsReleaseEnvironmentLocked(accountID, projectID, environment) {
		return nil, false, ErrNotFound
	}
	release, ok := m.projectReleaseSets[releaseSetID]
	if !ok || !release.Active || release.AccountID != accountID || release.ProjectID != projectID || release.EnvironmentSlug != environment {
		return nil, false, ErrConflict
	}
	return m.projectEnvironmentWorkloadConfigHashesLocked(accountID, projectID, environment, release.Members)
}

func (m *MemStore) projectEnvironmentWorkloadConfigHashesLocked(accountID, projectID, environment string, members []ProjectReleaseMember) (map[string]string, bool, error) {
	source, err := m.projectEnvironmentBySlugLocked(projectID, environment)
	if err != nil || source.AccountID != accountID {
		return nil, false, ErrNotFound
	}
	flagHash, err := FeatureFlagsQualificationHash(m.latestFeatureFlagsLocked(FeatureFlagScope{AccountID: accountID, ProjectID: projectID, EnvironmentID: source.ID}))
	if err != nil {
		return nil, false, err
	}
	hashes := make(map[string]string, len(members))
	scoped := flagHash != ""
	for _, member := range members {
		app, ok := m.apps[member.AppID]
		if !ok || app.Status == AppDeleted || app.AccountID != accountID || app.ProjectID != projectID {
			return nil, false, ErrConflict
		}
		env, err := m.workloadSpecEnvironmentLocked(accountID, projectID, environment, app.ID)
		if err != nil {
			return nil, false, err
		}
		desired := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadHeads[workloadSpecHeadKey(env.ID, app.ID)]]
		pinned := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadDeploymentSpecs[member.DeploymentID]]
		if pinned.ID != "" && (pinned.AppID != app.ID || pinned.EnvironmentID != env.ID) {
			return nil, false, ErrConflict
		}
		legacy, err := WorkloadSettingsFromApp(app)
		if err != nil {
			return nil, false, err
		}
		if route, ok := m.projectEnvironmentRoutePolicies[projectEnvironmentRoutePolicyKey(app.ID, environment)]; ok {
			legacy.OnlyAllowDeclaredRoutes, legacy.DeclaredRoutes = route.OnlyAllowDeclaredRoutes, cloneDeclaredRoutes(route.DeclaredRoutes)
		}
		hash, err := qualificationWorkloadHash(desired, pinned, legacy)
		if err != nil {
			return nil, false, err
		}
		hashes[app.Slug], err = api.QualificationWorkloadConfigHash(hash, flagHash)
		if err != nil {
			return nil, false, ErrConflict
		}
		scoped = scoped || desired.ID != ""
	}
	if len(hashes) == 0 {
		return nil, false, ErrConflict
	}
	return hashes, scoped, nil
}

func workloadSpecOrZero(spec ProjectEnvironmentWorkloadSpec, err error) (ProjectEnvironmentWorkloadSpec, error) {
	if errors.Is(err, ErrNotFound) {
		return ProjectEnvironmentWorkloadSpec{}, nil
	}
	return spec, err
}
