package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrProjectEnvironmentCloneManagedBindings = errors.New("state: project environment clone requires managed binding recreation")
	ErrProjectEnvironmentCloneQuota           = errors.New("state: project environment clone exceeds scoped configuration quota")
)

// ProjectEnvironmentClone describes one source-to-target environment clone.
// The scoped state rows are committed atomically by each state store.
type ProjectEnvironmentClone struct {
	AccountID                  string
	ProjectID                  string
	SourceSlug                 string
	TargetSlug                 string
	TargetProtected            bool
	ShareResources             bool
	ManagedBindingsPrepared    bool
	PreparedManagedBindingIDs  []string
	PreparedManagedSecretCount int
}

// ProjectEnvironmentCloneResult contains non-secret copy counts.
type ProjectEnvironmentCloneResult struct {
	ConfigurationCopied bool
	VariablesCopied     int
	SecretsCopied       int
	WorkloadsCopied     int
	BindingsCopied      int
	SharedResources     []string
}

// ProjectEnvironmentCloneManagedBindingsError prevents provider credentials
// from being duplicated as if they were ordinary customer secrets.
type ProjectEnvironmentCloneManagedBindingsError struct {
	ManagedSecretCount int
}

func (e *ProjectEnvironmentCloneManagedBindingsError) Error() string {
	return fmt.Sprintf("%v: %d managed secret target(s)", ErrProjectEnvironmentCloneManagedBindings, e.ManagedSecretCount)
}

func (e *ProjectEnvironmentCloneManagedBindingsError) Unwrap() error {
	return ErrProjectEnvironmentCloneManagedBindings
}

// ProjectEnvironmentCloneQuotaError identifies the workload and resource cap
// that would be exceeded before any target rows are written.
type ProjectEnvironmentCloneQuotaError struct {
	WorkloadSlug string
	Resource     string
	Limit        int
	Observed     int
}

func (e *ProjectEnvironmentCloneQuotaError) Error() string {
	return fmt.Sprintf("%v: workload=%s resource=%s limit=%d observed=%d", ErrProjectEnvironmentCloneQuota, e.WorkloadSlug, e.Resource, e.Limit, e.Observed)
}

func (e *ProjectEnvironmentCloneQuotaError) Unwrap() error {
	return ErrProjectEnvironmentCloneQuota
}

func (m *MemStore) CloneProjectEnvironment(_ context.Context, clone ProjectEnvironmentClone, limits api.Limits) (ProjectEnvironment, ProjectEnvironmentCloneResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	project, ok := m.projects[clone.ProjectID]
	if !ok || project.AccountID != clone.AccountID {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrNotFound
	}
	if _, err := m.projectEnvironmentBySlugLocked(clone.ProjectID, clone.SourceSlug); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	if _, err := m.projectEnvironmentBySlugLocked(clone.ProjectID, clone.TargetSlug); err == nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
	}
	apps := m.projectCloneAppsLocked(clone.ProjectID)
	for _, env := range m.envs {
		if _, ok := apps[env.AppID]; ok && env.Scope == clone.TargetSlug {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
		}
	}
	preparedBindings := make(map[string]struct{}, len(clone.PreparedManagedBindingIDs))
	for _, id := range clone.PreparedManagedBindingIDs {
		preparedBindings[id] = struct{}{}
	}
	if clone.ManagedBindingsPrepared && len(preparedBindings) != len(clone.PreparedManagedBindingIDs) {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
	}
	if clone.ManagedBindingsPrepared {
		sourceBindings := map[string]struct{}{}
		for _, secret := range m.secrets {
			if _, ok := apps[secret.AppID]; !ok || secret.Scope != clone.SourceSlug {
				continue
			}
			if secret.ManagedPostgresBindingID != "" {
				sourceBindings[secret.ManagedPostgresBindingID] = struct{}{}
			}
			if secret.ManagedObjectStorageCredentialID != "" {
				sourceBindings[secret.ManagedObjectStorageCredentialID] = struct{}{}
			}
		}
		if len(sourceBindings) != len(preparedBindings) {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
		}
	}
	preparedSecrets := 0
	preparedSecretBindings := map[string]struct{}{}
	for _, secret := range m.secrets {
		if _, ok := apps[secret.AppID]; !ok || secret.Scope != clone.TargetSlug {
			continue
		}
		if !clone.ManagedBindingsPrepared || (secret.ManagedPostgresBindingID == "") == (secret.ManagedObjectStorageCredentialID == "") {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
		}
		preparedID := secret.ManagedPostgresBindingID
		if preparedID == "" {
			preparedID = secret.ManagedObjectStorageCredentialID
		}
		if _, ok := preparedBindings[preparedID]; !ok {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
		}
		preparedSecretBindings[preparedID] = struct{}{}
		preparedSecrets++
	}
	if preparedSecrets != clone.PreparedManagedSecretCount || len(preparedSecretBindings) != len(preparedBindings) {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
	}
	if clone.ManagedBindingsPrepared && len(preparedBindings) == 0 {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
	}
	if managed := m.projectCloneManagedSecretCountLocked(apps, clone.SourceSlug); managed > 0 && !clone.ShareResources && !clone.ManagedBindingsPrepared {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, &ProjectEnvironmentCloneManagedBindingsError{ManagedSecretCount: managed}
	}
	if err := m.checkProjectCloneQuotaLocked(apps, clone.SourceSlug, clone.ManagedBindingsPrepared, limits); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	now := time.Now().UTC()
	created := ProjectEnvironment{
		ID: newID(), AccountID: clone.AccountID, ProjectID: clone.ProjectID,
		Slug: clone.TargetSlug, Protected: clone.TargetProtected,
		CreatedAt: now, UpdatedAt: now,
	}
	m.projectEnvironments[created.ID] = created
	result := m.copyProjectEnvironmentConfigLocked(clone, created.CreatedAt)
	result.WorkloadsCopied = len(apps)
	result.VariablesCopied = m.copyProjectEnvironmentVariablesLocked(apps, clone.SourceSlug, clone.TargetSlug, created.CreatedAt)
	result.SecretsCopied = m.copyProjectEnvironmentSecretsLocked(apps, clone.SourceSlug, clone.TargetSlug, created.CreatedAt)
	return created, result, nil
}

func (m *MemStore) projectCloneAppsLocked(projectID string) map[string]string {
	apps := map[string]string{}
	for _, app := range m.apps {
		if app.ProjectID == projectID && app.Status != AppDeleted {
			apps[app.ID] = app.Slug
		}
	}
	return apps
}

func (m *MemStore) projectCloneManagedSecretCountLocked(apps map[string]string, source string) int {
	count := 0
	for _, secret := range m.secrets {
		if _, ok := apps[secret.AppID]; ok && secret.Scope == source &&
			(secret.ManagedPostgresBindingID != "" || secret.ManagedObjectStorageCredentialID != "") {
			count++
		}
	}
	return count
}

func (m *MemStore) checkProjectCloneQuotaLocked(apps map[string]string, source string, managedBindingsPrepared bool, limits api.Limits) error {
	for appID, slug := range apps {
		secretCount, sourceSecrets, envCount, sourceEnv := 0, 0, 0, 0
		for _, secret := range m.secrets {
			if secret.AppID == appID {
				secretCount++
				if secret.Scope == source {
					sourceSecrets++
				}
			}
		}
		for _, env := range m.envs {
			if env.AppID == appID {
				envCount++
				if env.Scope == source {
					sourceEnv++
				}
			}
		}
		observedSecrets := secretCount + sourceSecrets
		if managedBindingsPrepared {
			for _, secret := range m.secrets {
				if secret.AppID == appID && secret.Scope == source && (secret.ManagedPostgresBindingID != "" || secret.ManagedObjectStorageCredentialID != "") {
					observedSecrets--
				}
			}
		}
		if limits.SecretCountMax > 0 && observedSecrets > limits.SecretCountMax {
			return &ProjectEnvironmentCloneQuotaError{WorkloadSlug: slug, Resource: "secrets", Limit: limits.SecretCountMax, Observed: observedSecrets}
		}
		if limits.EnvVarsMax > 0 && envCount+sourceEnv > limits.EnvVarsMax {
			return &ProjectEnvironmentCloneQuotaError{WorkloadSlug: slug, Resource: "variables", Limit: limits.EnvVarsMax, Observed: envCount + sourceEnv}
		}
	}
	return nil
}

func (m *MemStore) copyProjectEnvironmentConfigLocked(clone ProjectEnvironmentClone, now time.Time) ProjectEnvironmentCloneResult {
	source := m.projectEnvironmentConfigs[projectEnvironmentConfigKey(clone.ProjectID, clone.SourceSlug)]
	if len(source) == 0 {
		return ProjectEnvironmentCloneResult{}
	}
	config := cloneProjectEnvironmentConfig(source[len(source)-1])
	config.ID, config.EnvironmentSlug, config.Version, config.CreatedAt = newID(), clone.TargetSlug, 1, now
	m.projectEnvironmentConfigs[projectEnvironmentConfigKey(clone.ProjectID, clone.TargetSlug)] = []ProjectEnvironmentConfig{config}
	return ProjectEnvironmentCloneResult{ConfigurationCopied: true}
}

func (m *MemStore) copyProjectEnvironmentVariablesLocked(apps map[string]string, source, target string, now time.Time) int {
	rows := make([]AppEnv, 0)
	for _, env := range m.envs {
		if _, ok := apps[env.AppID]; ok && env.Scope == source {
			rows = append(rows, env)
		}
	}
	for _, env := range rows {
		env.Scope, env.CreatedAt, env.UpdatedAt = target, now, now
		m.envs[envKey{AppID: env.AppID, Scope: target, Key: env.Key}] = env
	}
	return len(rows)
}

func (m *MemStore) copyProjectEnvironmentSecretsLocked(apps map[string]string, source, target string, now time.Time) int {
	rows := make([]AppSecret, 0)
	for _, secret := range m.secrets {
		if _, ok := apps[secret.AppID]; ok && secret.Scope == source &&
			secret.ManagedPostgresBindingID == "" && secret.ManagedObjectStorageCredentialID == "" {
			rows = append(rows, secret)
		}
	}
	for _, secret := range rows {
		secret.Scope, secret.CreatedAt, secret.UpdatedAt = target, now, now
		secret.Ciphertext = append([]byte(nil), secret.Ciphertext...)
		m.secrets[secretKey{AppID: secret.AppID, Scope: target, Key: secret.Key}] = secret
	}
	return len(rows)
}
