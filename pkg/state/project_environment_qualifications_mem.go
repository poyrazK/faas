package state

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) CreateProjectEnvironmentQualification(_ context.Context, accountID, projectID, environment, releaseSetID string, configurationVersion int64, configurationHash string, secretRevisionHashes map[string]string, checks []ProjectEnvironmentQualificationCheck) (ProjectEnvironmentQualification, error) {
	if err := validateProjectEnvironmentQualificationScope(accountID, projectID, environment, releaseSetID); err != nil {
		return ProjectEnvironmentQualification{}, err
	}
	if configurationVersion < 0 || !api.ValidProjectEnvironmentConfigHash(configurationHash) {
		return ProjectEnvironmentQualification{}, ErrInvalidArgument
	}
	normalized, status, err := normalizeProjectEnvironmentQualificationChecks(checks)
	if err != nil {
		return ProjectEnvironmentQualification{}, err
	}
	normalizedSecretRevisionHashes, err := normalizeProjectEnvironmentQualificationSecretRevisionHashes(secretRevisionHashes, normalized)
	if err != nil {
		return ProjectEnvironmentQualification{}, err
	}
	releaseSetID = uuid.MustParse(releaseSetID).String()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ownsReleaseEnvironmentLocked(accountID, projectID, environment) {
		return ProjectEnvironmentQualification{}, ErrNotFound
	}
	release, ok := m.projectReleaseSets[releaseSetID]
	if !ok || !release.Active || release.AccountID != accountID || release.ProjectID != projectID || release.EnvironmentSlug != environment ||
		m.activeProjectReleaseSets[releaseKey(projectID, environment)] != releaseSetID {
		return ProjectEnvironmentQualification{}, ErrConflict
	}
	currentConfig := m.projectEnvironmentConfigLatestLocked(projectID, environment)
	if currentConfig.Version != configurationVersion || currentConfig.ConfigHash != configurationHash {
		return ProjectEnvironmentQualification{}, ErrConflict
	}
	currentSecretRevisionHashes, err := m.projectEnvironmentSecretRevisionHashesLocked(accountID, environment, release.Members)
	if err != nil {
		return ProjectEnvironmentQualification{}, err
	}
	if !sameProjectEnvironmentSecretRevisionHashes(normalizedSecretRevisionHashes, currentSecretRevisionHashes) {
		return ProjectEnvironmentQualification{}, ErrConflict
	}
	members := make(map[string]string, len(release.Members))
	for _, member := range release.Members {
		app, exists := m.apps[member.AppID]
		if !exists || app.AccountID != accountID || app.ProjectID != projectID {
			return ProjectEnvironmentQualification{}, ErrConflict
		}
		members[app.Slug] = member.DeploymentID
	}
	for _, check := range normalized {
		if len(check.Results) != len(members) {
			return ProjectEnvironmentQualification{}, ErrInvalidArgument
		}
		for _, result := range check.Results {
			if members[result.WorkloadSlug] != result.DeploymentID {
				return ProjectEnvironmentQualification{}, ErrInvalidArgument
			}
		}
	}
	now := time.Now().UTC()
	qualification := ProjectEnvironmentQualification{
		ID: uuid.NewString(), AccountID: accountID, ProjectID: projectID,
		EnvironmentSlug: environment, ReleaseSetID: releaseSetID,
		ConfigurationVersion: configurationVersion, ConfigurationHash: configurationHash,
		SecretRevisionHashes: normalizedSecretRevisionHashes, Status: status,
		Checks:    append([]ProjectEnvironmentQualificationCheck(nil), normalized...),
		CreatedAt: now, ExpiresAt: now.Add(ProjectEnvironmentQualificationTTL),
	}
	m.projectEnvironmentQualifications[releaseSetID] = append(m.projectEnvironmentQualifications[releaseSetID], cloneProjectEnvironmentQualification(qualification))
	return cloneProjectEnvironmentQualification(qualification), nil
}

func (m *MemStore) LatestProjectEnvironmentQualification(_ context.Context, accountID, projectID, environment, releaseSetID string) (ProjectEnvironmentQualification, error) {
	if err := validateProjectEnvironmentQualificationScope(accountID, projectID, environment, releaseSetID); err != nil {
		return ProjectEnvironmentQualification{}, err
	}
	releaseSetID = uuid.MustParse(releaseSetID).String()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ownsReleaseEnvironmentLocked(accountID, projectID, environment) {
		return ProjectEnvironmentQualification{}, ErrNotFound
	}
	records := m.projectEnvironmentQualifications[releaseSetID]
	if len(records) == 0 {
		return ProjectEnvironmentQualification{}, ErrNotFound
	}
	latest := records[len(records)-1]
	if latest.AccountID != accountID || latest.ProjectID != projectID || latest.EnvironmentSlug != environment {
		return ProjectEnvironmentQualification{}, ErrNotFound
	}
	return cloneProjectEnvironmentQualification(latest), nil
}

func cloneProjectEnvironmentQualification(qualification ProjectEnvironmentQualification) ProjectEnvironmentQualification {
	secretRevisionHashes := make(map[string]string, len(qualification.SecretRevisionHashes))
	for workload, hash := range qualification.SecretRevisionHashes {
		secretRevisionHashes[workload] = hash
	}
	qualification.SecretRevisionHashes = secretRevisionHashes
	checks := make([]ProjectEnvironmentQualificationCheck, len(qualification.Checks))
	for i, check := range qualification.Checks {
		checks[i] = check
		checks[i].Results = append([]ProjectEnvironmentQualificationResult(nil), check.Results...)
		for j, result := range checks[i].Results {
			if result.HTTPStatus != nil {
				status := *result.HTTPStatus
				checks[i].Results[j].HTTPStatus = &status
			}
		}
	}
	qualification.Checks = checks
	return qualification
}

func (m *MemStore) projectEnvironmentSecretRevisionHashesLocked(accountID, environment string, members []ProjectReleaseMember) (map[string]string, error) {
	revisionsByWorkload := make(map[string][]api.ProjectEnvironmentSecretRevision, len(members))
	for _, member := range members {
		app, ok := m.apps[member.AppID]
		if !ok || app.AccountID != accountID {
			return nil, ErrConflict
		}
		revisionsByWorkload[app.Slug] = nil
		for address, secret := range m.secrets {
			if address.AppID != app.ID || address.Scope != environment || secret.AccountID != accountID {
				continue
			}
			revisionsByWorkload[app.Slug] = append(revisionsByWorkload[app.Slug], api.ProjectEnvironmentSecretRevision{
				Key: address.Key, Version: secret.SecretVersion,
				ManagedBy:            projectEnvironmentSecretManagedBy(secret),
				BindingID:            projectEnvironmentSecretBindingID(secret),
				CredentialGeneration: secret.ManagedCredentialGeneration,
			})
		}
	}
	hashes := make(map[string]string, len(revisionsByWorkload))
	for workload, revisions := range revisionsByWorkload {
		hash, err := api.ProjectEnvironmentSecretRevisionHash(revisions)
		if err != nil {
			return nil, fmt.Errorf("state: fingerprint environment secret revisions: %w", err)
		}
		hashes[workload] = hash
	}
	return hashes, nil
}

func projectEnvironmentSecretManagedBy(secret AppSecret) string {
	switch {
	case secret.ManagedPostgresBindingID != "":
		return "managed_postgres"
	case secret.ManagedObjectStorageCredentialID != "":
		return "object_storage"
	default:
		return ""
	}
}

func projectEnvironmentSecretBindingID(secret AppSecret) string {
	if secret.ManagedPostgresBindingID != "" {
		return secret.ManagedPostgresBindingID
	}
	return secret.ManagedObjectStorageCredentialID
}
