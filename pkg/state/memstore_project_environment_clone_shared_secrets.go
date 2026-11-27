// adr: 590
package state

import (
	"bytes"
	"time"
)

// cloneSharedSecretsLocked reads an environment's shared secrets and every
// project app's references and suppressions in it; it mirrors
// readCloneSharedSecretsDB.
func (m *MemStore) cloneSharedSecretsLocked(accountID, projectID, environment string) (projectCloneSharedSecrets, error) {
	env, err := m.projectEnvironmentBySlugLocked(projectID, environment)
	if err != nil {
		return projectCloneSharedSecrets{}, err
	}
	out := projectCloneSharedSecrets{}
	for _, s := range m.projectEnvironmentSecretsLocked(projectID, environment) {
		if s.AccountID != accountID {
			continue
		}
		out.Secrets = append(out.Secrets, projectCloneSharedSecret{Key: s.Key, Ciphertext: bytes.Clone(s.Ciphertext), Kid: s.Kid, ValueHash: s.ValueHash, SecretVersion: s.SecretVersion})
	}
	projectApp := func(appID string) bool {
		app, ok := m.apps[appID]
		return ok && app.ProjectID == projectID && app.AccountID == accountID && app.Status != AppDeleted && app.PreviewOfSlug == ""
	}
	for key, row := range m.appEnvironmentSecretRefs {
		if key.EnvironmentID == env.ID && projectApp(key.AppID) {
			out.Refs = append(out.Refs, projectCloneSecretRef{AppID: key.AppID, Key: key.Key, SecretName: row.Ref})
		}
	}
	for key := range m.appEnvironmentSecretSuppressions {
		if key.EnvironmentID == env.ID && projectApp(key.AppID) {
			out.Suppressions = append(out.Suppressions, projectCloneSecretSuppression{AppID: key.AppID, Key: key.Key})
		}
	}
	return normalizeCloneSharedSecrets(out)
}

// materializeCloneSharedSecretsLocked writes captured shared secrets,
// references and suppressions into the target environment; it mirrors
// materializeCloneSharedSecretsTx.
func (m *MemStore) materializeCloneSharedSecretsLocked(clone ProjectEnvironmentClone, target ProjectEnvironment, captured projectCloneSharedSecrets, now time.Time) (secrets, refs int) {
	for _, s := range captured.Secrets {
		if m.projectEnvironmentSecrets == nil {
			m.projectEnvironmentSecrets = map[string]ProjectEnvironmentSecret{}
		}
		m.projectEnvironmentSecrets[projectEnvironmentSecretKey(clone.ProjectID, target.Slug, s.Key)] = ProjectEnvironmentSecret{
			AccountID: clone.AccountID, ProjectID: clone.ProjectID, EnvironmentSlug: target.Slug, Key: s.Key,
			Ciphertext: bytes.Clone(s.Ciphertext), Kid: s.Kid, ValueHash: s.ValueHash, SecretVersion: s.SecretVersion, CreatedAt: now, UpdatedAt: now}
		m.advanceProjectSharedSecretGenerationLocked(clone.ProjectID, target.Slug)
	}
	if len(captured.Secrets) > 0 {
		m.projectRuntimeChangeLocked(clone.ProjectID, target.Slug, now)
	}
	for _, r := range captured.Refs {
		app, ok := m.eventSubscriptionAppLocked(r.AppID)
		if !ok {
			continue
		}
		if m.appEnvironmentSecretRefs == nil {
			m.appEnvironmentSecretRefs = map[environmentSecretRefKey]environmentSecretRef{}
		}
		m.appEnvironmentSecretRefs[environmentSecretRefKey{app.ID, target.ID, r.Key}] = environmentSecretRef{Ref: r.SecretName, UpdatedAt: now}
		m.markEnvironmentRuntimeChangedAndSnapshotsLocked(app.ID, target.Slug, now)
		refs++
	}
	for _, s := range captured.Suppressions {
		app, ok := m.eventSubscriptionAppLocked(s.AppID)
		if !ok {
			continue
		}
		if m.appEnvironmentSecretSuppressions == nil {
			m.appEnvironmentSecretSuppressions = map[environmentSecretRefKey]time.Time{}
		}
		m.appEnvironmentSecretSuppressions[environmentSecretRefKey{app.ID, target.ID, s.Key}] = now
		m.markEnvironmentRuntimeChangedAndSnapshotsLocked(app.ID, target.Slug, now)
	}
	return len(captured.Secrets), refs
}

// verifyCloneSharedSecretsPublicationLocked proves the stage holds exactly
// the captured shared secrets.
func (m *MemStore) verifyCloneSharedSecretsPublicationLocked(op ProjectEnvironmentCloneOperation, records []projectCloneWorkloadRecord) error {
	if len(records) == 0 {
		return nil
	}
	captured, err := capturedCloneSharedSecrets(records)
	if err != nil || captured == nil {
		return err
	}
	target, err := m.cloneSharedSecretsLocked(op.AccountID, op.ProjectID, op.TargetEnvironment)
	if err != nil {
		return err
	}
	return validateCloneSharedSecretsProof(captured, target)
}
