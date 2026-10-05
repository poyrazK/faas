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
	// Optional preparation fence. Each source workload's effective runtime
	// value scope is checked again before any target configuration is written.
	ExpectedSourceValueScopes map[string]string
	ExpectedSourceValuesHash  string
	// A durable clone alone may materialize its reserved target. The revision
	// fences workers which lost ownership while preparing provider resources.
	CloneOperationID       string
	CloneOperationRevision int64
	sourceValueScopesJSON  []byte
	capturedValues         map[string]projectCloneWorkloadValues
	capturedValueScopes    map[string]string
	capturedPolicies       map[string]projectCloneScopedPolicies
}

// ProjectEnvironmentCloneResult contains non-secret copy counts.
type ProjectEnvironmentCloneResult struct {
	ConfigurationCopied    bool
	VariablesCopied        int
	SecretsCopied          int
	SecretReferencesCopied int
	WorkloadsCopied        int
	BindingsCopied         int
	RoutesCopied           int
	PoliciesCopied         int
	SharedResources        []string
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

func (m *MemStore) CloneProjectEnvironment(ctx context.Context, clone ProjectEnvironmentClone, limits api.Limits) (ProjectEnvironment, ProjectEnvironmentCloneResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	project, ok := m.projects[clone.ProjectID]
	if !ok || project.AccountID != clone.AccountID {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrNotFound
	}
	if err := m.checkProjectEnvironmentCloneReservationLocked(clone); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	source, err := m.projectEnvironmentBySlugLocked(clone.ProjectID, clone.SourceSlug)
	if err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	if _, err := m.projectEnvironmentBySlugLocked(clone.ProjectID, clone.TargetSlug); err == nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
	}
	apps := m.projectCloneAppsLocked(clone.ProjectID)
	if clone.CloneOperationID != "" {
		var records []projectCloneWorkloadRecord
		clone.capturedValueScopes = map[string]string{}
		for _, record := range m.projectEnvironmentCloneWorkloads[clone.CloneOperationID] {
			record, err := copyCloneWorkloadRecord(record)
			if err != nil {
				return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
			}
			records = append(records, record)
			clone.capturedValueScopes[record.AppID] = record.SourceScope
		}
		clone.capturedValues, err = capturedCloneValues(records)
		if err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
		clone.capturedPolicies, err = capturedCloneScopedPolicies(records)
		if err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
	}
	valueScopes, err := m.projectCloneValueScopesLocked(apps, clone)
	if err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	if clone.ExpectedSourceValuesHash != "" {
		var hash string
		if clone.capturedValues != nil {
			hash, err = clone.capturedValuesHash(valueScopes)
		} else {
			hash, err = m.projectCloneValuesHashLocked(valueScopes)
		}
		if err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
		if hash != clone.ExpectedSourceValuesHash {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
		}
	}
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
			if _, ok := apps[secret.AppID]; !ok || secret.Scope != valueScopes[secret.AppID] {
				continue
			}
			if secret.ManagedPostgresBindingID != "" {
				sourceBindings[secret.ManagedPostgresBindingID] = struct{}{}
			}
			if secret.ManagedObjectStorageCredentialID != "" {
				sourceBindings[secret.ManagedObjectStorageCredentialID] = struct{}{}
			}
		}
		if clone.capturedValues != nil {
			sourceBindings = map[string]struct{}{}
			for _, values := range clone.capturedValues {
				for _, secret := range values.Secrets {
					if id := cloneSecretManagedID(secret); id != "" {
						sourceBindings[id] = struct{}{}
					}
				}
			}
		}
		if len(sourceBindings) != len(preparedBindings) {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
		}
		if clone.CloneOperationID != "" {
			if err := m.checkCapturedClonePostgresPreparationsLocked(clone); err != nil {
				return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
			}
			if err := m.checkCapturedCloneObjectPreparationsLocked(clone); err != nil {
				return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
			}
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
	managed := m.projectCloneManagedSecretCountLocked(apps, valueScopes)
	if clone.capturedValues != nil {
		managed = 0
		for _, values := range clone.capturedValues {
			for _, secret := range values.Secrets {
				if cloneSecretManagedID(secret) != "" {
					managed++
				}
			}
		}
	}
	if managed > 0 && !clone.ShareResources && !clone.ManagedBindingsPrepared {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, &ProjectEnvironmentCloneManagedBindingsError{ManagedSecretCount: managed}
	}
	if err := m.checkProjectCloneQuotaLocked(apps, valueScopes, clone.ManagedBindingsPrepared, limits, clone.capturedValues); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	if err := m.validateProjectCloneTrafficPoliciesLocked(apps, clone.SourceSlug, clone.TargetSlug); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	settings := make(map[string]ProjectEnvironmentWorkloadSettings, len(apps))
	var capturedRecords []projectCloneWorkloadRecord
	var frozenConfig *projectCloneProjectConfig
	if clone.CloneOperationID != "" && len(m.projectEnvironmentCloneWorkloads[clone.CloneOperationID]) != len(apps) {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
	}
	for appID := range apps {
		var captured ProjectEnvironmentWorkloadSettings
		if clone.CloneOperationID != "" {
			record, ok := m.projectEnvironmentCloneWorkloads[clone.CloneOperationID][appID]
			if !ok {
				return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
			}
			record, err = copyCloneWorkloadRecord(record)
			if err == nil {
				captured, err = cloneWorkloadSettings(record.snapshot.Settings)
				capturedRecords = append(capturedRecords, record)
			}
		} else if specID := m.projectEnvironmentWorkloadHeads[workloadSpecHeadKey(source.ID, appID)]; specID != "" {
			spec := m.projectEnvironmentWorkloadSpecs[specID]
			hash, hashErr := WorkloadSettingsHash(spec.Settings)
			if hashErr != nil || hash != spec.Hash {
				return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
			}
			captured, err = cloneWorkloadSettings(spec.Settings)
		} else {
			captured, err = WorkloadSettingsFromApp(m.apps[appID])
			if policy, found := m.projectEnvironmentRoutePolicies[projectEnvironmentRoutePolicyKey(appID, clone.SourceSlug)]; found {
				captured.OnlyAllowDeclaredRoutes, captured.DeclaredRoutes = policy.OnlyAllowDeclaredRoutes, cloneDeclaredRoutes(policy.DeclaredRoutes)
			}
		}
		if err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
		settings[appID] = captured
	}
	if len(capturedRecords) > 0 {
		config, err := capturedCloneProjectConfig(capturedRecords)
		if err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
		frozenConfig = &config
	}
	var flagSnapshot projectCloneFeatureFlags
	if clone.CloneOperationID != "" {
		if frozenConfig == nil || frozenConfig.FeatureFlags == nil || frozenConfig.FeatureFlags.SourceEnvironmentID != source.ID {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
		}
		flagSnapshot = *frozenConfig.FeatureFlags
	} else {
		flagSnapshot, err = captureCloneFeatureFlags(m.latestFeatureFlagsLocked(FeatureFlagScope{AccountID: clone.AccountID, ProjectID: clone.ProjectID, EnvironmentID: source.ID}))
		if err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
	}
	now := time.Now().UTC()
	created := ProjectEnvironment{
		ID: newID(), AccountID: clone.AccountID, ProjectID: clone.ProjectID,
		Slug: clone.TargetSlug, Protected: clone.TargetProtected,
		CreatedAt: now, UpdatedAt: now,
	}
	change := memTrafficPolicyChange{Environments: map[string]ProjectEnvironment{created.ID: created}, Policies: make(map[string]ProjectEnvironmentEdgePolicy)}
	for appID := range apps {
		if policy, present := m.projectEnvironmentEdgePolicies[projectEnvironmentRoutePolicyKey(appID, clone.SourceSlug)]; present {
			policy.EnvironmentSlug, policy.CreatedAt, policy.UpdatedAt = clone.TargetSlug, now, now
			change.Policies[projectEnvironmentRoutePolicyKey(appID, clone.TargetSlug)] = policy
		}
	}
	if err := m.validateMemTrafficPolicyChangeLocked(ctx, clone.AccountID, change); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	m.projectEnvironments[created.ID] = created
	m.copyCloneFeatureFlagsLocked(created, flagSnapshot)
	for appID, captured := range settings {
		hash, _ := WorkloadSettingsHash(captured) // validated before any target writes
		spec := ProjectEnvironmentWorkloadSpec{
			ID: newID(), AccountID: clone.AccountID, ProjectID: clone.ProjectID,
			EnvironmentID: created.ID, EnvironmentSlug: created.Slug, AppID: appID,
			Revision: 1, Hash: hash, Settings: captured, CreatedAt: now,
		}
		m.projectEnvironmentWorkloadSpecs[spec.ID] = spec
		m.projectEnvironmentWorkloadHeads[workloadSpecHeadKey(created.ID, appID)] = spec.ID
	}
	result := m.copyProjectEnvironmentConfigLocked(clone, created.CreatedAt, frozenConfig)
	result.WorkloadsCopied = len(apps)
	if clone.capturedValues != nil {
		result.VariablesCopied, result.SecretsCopied = m.copyCapturedCloneValuesLocked(clone, created.CreatedAt)
	} else {
		result.VariablesCopied = m.copyProjectEnvironmentVariablesLocked(apps, valueScopes, clone.TargetSlug, created.CreatedAt)
		result.SecretsCopied = m.copyProjectEnvironmentSecretsLocked(apps, valueScopes, clone.TargetSlug, created.CreatedAt)
	}
	if clone.capturedValues == nil {
		result.SecretReferencesCopied = m.copyProjectEnvironmentSecretReferencesLocked(apps, source.ID, created, now)
		m.copyProjectEnvironmentSecretSuppressionsLocked(apps, source.ID, created, now)
	}
	if clone.capturedPolicies != nil {
		m.copyCapturedScopedPoliciesLocked(clone, created.CreatedAt, &result)
		if result.RoutesCopied < result.WorkloadsCopied {
			result.SharedResources = append(result.SharedResources, "routes")
		}
		result.SharedResources = append(result.SharedResources, "policies")
		return created, result, nil
	}
	for appID := range apps {
		policy, copyable := m.projectCloneRoutePolicyLocked(appID, clone.SourceSlug)
		if !copyable {
			continue // The app-wide OpenAPI document is not cloneable route structure.
		}
		policy.EnvironmentSlug, policy.CreatedAt, policy.UpdatedAt = clone.TargetSlug, created.CreatedAt, created.CreatedAt
		policy.DeclaredRoutes = cloneDeclaredRoutes(policy.DeclaredRoutes)
		m.projectEnvironmentRoutePolicies[projectEnvironmentRoutePolicyKey(appID, clone.TargetSlug)] = policy
		result.RoutesCopied++
	}
	if result.RoutesCopied < result.WorkloadsCopied {
		result.SharedResources = append(result.SharedResources, "routes")
	}
	for appID := range apps {
		policy, ok := m.projectEnvironmentEdgePolicies[projectEnvironmentRoutePolicyKey(appID, clone.SourceSlug)]
		if !ok {
			continue
		}
		policy.EnvironmentSlug, policy.CreatedAt, policy.UpdatedAt = clone.TargetSlug, created.CreatedAt, created.CreatedAt
		policy.Rules = cloneProjectEnvironmentEdgeRules(policy.Rules)
		m.projectEnvironmentEdgePolicies[projectEnvironmentRoutePolicyKey(appID, clone.TargetSlug)] = policy
		result.PoliciesCopied++
	}
	// Only headers and CORS can be environment-owned in this slice. Other
	// edge-rule kinds remain application-wide even after an explicit clone.
	result.SharedResources = append(result.SharedResources, "policies")
	return created, result, nil
}

func (m *MemStore) projectCloneRoutePolicyLocked(appID, source string) (ProjectEnvironmentRoutePolicy, bool) {
	app := m.apps[appID]
	policy, ok := m.projectEnvironmentRoutePolicies[projectEnvironmentRoutePolicyKey(appID, source)]
	if !ok {
		policy.OnlyAllowDeclaredRoutes, policy.DeclaredRoutes = app.OnlyAllowDeclaredRoutes, app.DeclaredRoutes
	}
	policy.AccountID, policy.ProjectID, policy.AppID = app.AccountID, app.ProjectID, app.ID
	return policy, !policy.OnlyAllowDeclaredRoutes || len(policy.DeclaredRoutes) != 0
}

func (m *MemStore) validateProjectCloneTrafficPoliciesLocked(apps map[string]string, source, target string) error {
	for appID := range apps {
		if policy, copyable := m.projectCloneRoutePolicyLocked(appID, source); copyable {
			policy.EnvironmentSlug = target
			if err := validateMemTrafficProjection("environment_route_policy", environmentRouteTrafficProjection(policy)); err != nil {
				return err
			}
		}
		if policy, ok := m.projectEnvironmentEdgePolicies[projectEnvironmentRoutePolicyKey(appID, source)]; ok {
			policy.EnvironmentSlug = target
			if err := validateMemTrafficProjection("environment_edge_policy", environmentEdgeTrafficProjection(policy)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *MemStore) projectCloneAppsLocked(projectID string) map[string]string {
	apps := map[string]string{}
	for _, app := range m.apps {
		if app.ProjectID == projectID && app.Status != AppDeleted && app.PreviewOfSlug == "" {
			apps[app.ID] = app.Slug
		}
	}
	return apps
}

func (m *MemStore) projectCloneManagedSecretCountLocked(apps map[string]string, scopes map[string]string) int {
	count := 0
	for _, secret := range m.secrets {
		if _, ok := apps[secret.AppID]; ok && secret.Scope == scopes[secret.AppID] &&
			(secret.ManagedPostgresBindingID != "" || secret.ManagedObjectStorageCredentialID != "") {
			count++
		}
	}
	return count
}

func (m *MemStore) checkProjectCloneQuotaLocked(apps map[string]string, scopes map[string]string, managedBindingsPrepared bool, limits api.Limits, captured map[string]projectCloneWorkloadValues) error {
	for appID, slug := range apps {
		source := scopes[appID]
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
		for key := range m.appEnvironmentSecretRefs {
			if key.AppID == appID {
				envCount++
				if environment := m.projectEnvironments[key.EnvironmentID]; environment.Slug == source {
					sourceEnv++
				}
			}
		}
		if captured != nil {
			if err := checkCapturedCloneQuota(slug, captured[appID], secretCount, envCount, managedBindingsPrepared, limits); err != nil {
				return err
			}
			continue
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
		observedSuppressions := m.environmentSecretSuppressionCountLocked(appID) + len(m.environmentSecretSuppressionsLocked(appID, source))
		if observedSuppressions > api.EnvironmentSecretReferenceSuppressionsMaxPerApp {
			return &ProjectEnvironmentCloneQuotaError{WorkloadSlug: slug, Resource: "secret_reference_suppressions", Limit: api.EnvironmentSecretReferenceSuppressionsMaxPerApp, Observed: observedSuppressions}
		}
	}
	return nil
}

// A clone gets its own catalog identity and reference intent. Git source,
// ownership, overrides and runtime receipts belong to the original environment.
func (m *MemStore) copyProjectEnvironmentSecretReferencesLocked(apps map[string]string, sourceID string, target ProjectEnvironment, now time.Time) int {
	rows := map[environmentSecretRefKey]environmentSecretRef{}
	for key, row := range m.appEnvironmentSecretRefs {
		if _, ok := apps[key.AppID]; ok && key.EnvironmentID == sourceID {
			rows[environmentSecretRefKey{key.AppID, target.ID, key.Key}] = environmentSecretRef{Ref: row.Ref, UpdatedAt: now}
		}
	}
	for key, row := range rows {
		m.appEnvironmentSecretRefs[key] = row
		m.markEnvironmentRuntimeChangedAndSnapshotsLocked(key.AppID, target.Slug, now)
	}
	return len(rows)
}

func (m *MemStore) copyProjectEnvironmentSecretSuppressionsLocked(apps map[string]string, sourceID string, target ProjectEnvironment, now time.Time) {
	rows := map[environmentSecretRefKey]time.Time{}
	for key := range m.appEnvironmentSecretSuppressions {
		if _, ok := apps[key.AppID]; ok && key.EnvironmentID == sourceID {
			rows[environmentSecretRefKey{key.AppID, target.ID, key.Key}] = now
		}
	}
	for key := range rows {
		m.appEnvironmentSecretSuppressions[key] = now
		m.markEnvironmentRuntimeChangedAndSnapshotsLocked(key.AppID, target.Slug, now)
	}
}

func (m *MemStore) copyProjectEnvironmentConfigLocked(clone ProjectEnvironmentClone, now time.Time, frozen *projectCloneProjectConfig) ProjectEnvironmentCloneResult {
	if frozen != nil {
		config := ProjectEnvironmentConfig{ID: newID(), AccountID: clone.AccountID, ProjectID: clone.ProjectID,
			EnvironmentSlug: clone.TargetSlug, Version: 1, ConfigHash: frozen.Hash,
			Values: append([]byte(nil), frozen.Values...), CreatedAt: now}
		m.projectEnvironmentConfigs[projectEnvironmentConfigKey(clone.ProjectID, clone.TargetSlug)] = []ProjectEnvironmentConfig{config}
		return ProjectEnvironmentCloneResult{ConfigurationCopied: true}
	}
	source := m.projectEnvironmentConfigs[projectEnvironmentConfigKey(clone.ProjectID, clone.SourceSlug)]
	if len(source) == 0 {
		return ProjectEnvironmentCloneResult{}
	}
	config := cloneProjectEnvironmentConfig(source[len(source)-1])
	config.ID, config.EnvironmentSlug, config.Version, config.CreatedAt = newID(), clone.TargetSlug, 1, now
	m.projectEnvironmentConfigs[projectEnvironmentConfigKey(clone.ProjectID, clone.TargetSlug)] = []ProjectEnvironmentConfig{config}
	return ProjectEnvironmentCloneResult{ConfigurationCopied: true}
}

func (m *MemStore) copyProjectEnvironmentVariablesLocked(apps map[string]string, scopes map[string]string, target string, now time.Time) int {
	rows := make([]AppEnv, 0)
	for _, env := range m.envs {
		if _, ok := apps[env.AppID]; ok && env.Scope == scopes[env.AppID] {
			rows = append(rows, env)
		}
	}
	for _, env := range rows {
		env.Scope, env.CreatedAt, env.UpdatedAt = target, now, now
		m.envs[envKey{AppID: env.AppID, Scope: target, Key: env.Key}] = env
	}
	return len(rows)
}

func (m *MemStore) copyProjectEnvironmentSecretsLocked(apps map[string]string, scopes map[string]string, target string, now time.Time) int {
	rows := make([]AppSecret, 0)
	for _, secret := range m.secrets {
		if _, ok := apps[secret.AppID]; ok && secret.Scope == scopes[secret.AppID] &&
			secret.ManagedPostgresBindingID == "" && secret.ManagedObjectStorageCredentialID == "" {
			rows = append(rows, secret)
		}
	}
	for _, secret := range rows {
		secret.Scope, secret.CreatedAt, secret.UpdatedAt = target, now, now
		secret.Ciphertext = append([]byte(nil), secret.Ciphertext...)
		// The secret's known customer revision follows the clone. Runtime
		// delivery does not: the new scope must be started independently.
		secret.DeliveryVersion = 1
		secret.DeliveredVersion = 0
		secret.DeliveryStatus = SecretDeliveryPending
		secret.LastDeliveryAttemptAt = nil
		secret.LastDeliveredAt = nil
		secret.LastDeliveryErrorCode = ""
		secret.LastDeliveredWakeID = ""
		secret.LastDeliveredInstanceID = ""
		m.secrets[secretKey{AppID: secret.AppID, Scope: target, Key: secret.Key}] = secret
	}
	return len(rows)
}
