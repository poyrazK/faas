package state

// adr: 393

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ DeploymentRegistryVerificationStore = (*MemStore)(nil)

func (m *MemStore) RecordDeploymentRegistryVerification(ctx context.Context, input DeploymentRegistryVerificationInput) (DeploymentRegistryVerification, error) {
	in, hash, err := prepareRegistryVerification(input)
	if err != nil {
		return DeploymentRegistryVerification{}, err
	}
	if err := ctx.Err(); err != nil {
		return DeploymentRegistryVerification{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return DeploymentRegistryVerification{}, err
	}
	app, found, dep, exists := m.registryVerificationOwnerLocked(in.AppID, in.DeploymentID)
	if !found || !exists {
		return DeploymentRegistryVerification{}, ErrNotFound
	}
	if err := checkRegistryVerificationOwner(in, app, dep); err != nil {
		return DeploymentRegistryVerification{}, err
	}
	signer := m.trustedSigners[trustedSignerKey{AppID: app.ID, SignerName: in.Proof.PublisherName}]
	if !sameStandardUUID(signer.AccountID, in.AccountID) {
		signer.CosignPublicKey = nil
	}
	if err := verifyRegistryCurrentKey(in, signer.CosignPublicKey); err != nil {
		return DeploymentRegistryVerification{}, err
	}
	if existing, found := m.deploymentRegistryVerifications[in.ID]; found {
		if existing.InputHash != hash {
			return DeploymentRegistryVerification{}, ErrConflict
		}
		existing.Input = cloneRegistryVerificationInput(existing.Input)
		return existing, nil
	}
	if m.deploymentRegistryVerifications == nil {
		m.deploymentRegistryVerifications = map[string]DeploymentRegistryVerification{}
	}
	now := time.Now().UTC()
	value := DeploymentRegistryVerification{ID: in.ID, Input: in, InputHash: hash, VerifiedAt: now, ExpiresAt: now.Add(api.ImageSignatureVerificationTTL)}
	m.deploymentRegistryVerifications[in.ID] = value
	value.Input = cloneRegistryVerificationInput(value.Input)
	return value, nil
}

func (m *MemStore) GetLatestDeploymentRegistryVerification(ctx context.Context, accountID, appID, depID, workload string) (DeploymentRegistryVerification, error) {
	if !validStandardResourceRead(accountID, appID) || !validStandardResourceRead(depID, depID) {
		return DeploymentRegistryVerification{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return DeploymentRegistryVerification{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, found, dep, exists := m.registryVerificationOwnerLocked(appID, depID)
	if !found || !exists || !sameStandardUUID(app.AccountID, accountID) || !sameStandardUUID(dep.AppID, appID) || app.Status == AppDeleted {
		return DeploymentRegistryVerification{}, ErrNotFound
	}
	ref, err := registryWorkloadReference(dep, workload)
	if err != nil {
		return DeploymentRegistryVerification{}, ErrNotFound
	}
	var latest DeploymentRegistryVerification
	for _, value := range m.deploymentRegistryVerifications {
		in := value.Input
		if sameStandardUUID(in.AccountID, accountID) && sameStandardUUID(in.AppID, appID) && sameStandardUUID(in.DeploymentID, depID) && in.OrgID == registryCanonicalOrg(app.OrgID) && in.WorkloadName == workload && in.ImageReference == ref &&
			(latest.ID == "" || value.VerifiedAt.After(latest.VerifiedAt) || value.VerifiedAt.Equal(latest.VerifiedAt) && value.ID > latest.ID) {
			latest = value
		}
	}
	if latest.ID == "" {
		return latest, ErrNotFound
	}
	latest.Input = cloneRegistryVerificationInput(latest.Input)
	return latest, nil
}

func (m *MemStore) GetDeploymentRegistryVerificationByID(ctx context.Context, accountID, appID, depID, id string) (DeploymentRegistryVerification, error) {
	if !validStandardResourceRead(accountID, appID) || !validStandardResourceRead(depID, id) {
		return DeploymentRegistryVerification{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return DeploymentRegistryVerification{}, err
	}
	app, found, dep, exists := m.registryVerificationOwnerLocked(appID, depID)
	value, ok := m.deploymentRegistryVerifications[canonicalStandardUUID(id)]
	in := value.Input
	if !found || !exists || !ok || app.Status == AppDeleted || !sameStandardUUID(app.AccountID, accountID) || !sameStandardUUID(dep.AppID, appID) || in.AccountID != canonicalStandardUUID(accountID) || in.AppID != canonicalStandardUUID(appID) || in.DeploymentID != canonicalStandardUUID(depID) || in.OrgID != registryCanonicalOrg(app.OrgID) {
		return DeploymentRegistryVerification{}, ErrNotFound
	}
	ref, err := registryWorkloadReference(dep, in.WorkloadName)
	if err != nil || ref != in.ImageReference {
		return DeploymentRegistryVerification{}, ErrNotFound
	}
	if err := validateRegistryVerification(value); err != nil {
		return DeploymentRegistryVerification{}, err
	}
	value.Input = cloneRegistryVerificationInput(value.Input)
	return value, nil
}

// MemStore's legacy identities may omit UUID hyphens. Evidence hashes use the
// same canonical UUIDs as PostgreSQL without changing those map keys.
func (m *MemStore) registryVerificationOwnerLocked(appID, depID string) (App, bool, Deployment, bool) {
	var app App
	var dep Deployment
	found, exists := false, false
	for _, a := range m.apps {
		if sameStandardUUID(a.ID, appID) {
			app = a
			found = true
			break
		}
	}
	for _, d := range m.deployments {
		if sameStandardUUID(d.ID, depID) {
			dep = d
			exists = true
			break
		}
	}
	return app, found, dep, exists
}
