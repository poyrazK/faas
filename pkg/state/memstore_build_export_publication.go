package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"time"
)

var _ BuildExportPublicationStore = (*MemStore)(nil)

func (m *MemStore) HasBuildExportPublication(ctx context.Context, accountID, appID, depID string) (bool, error) {
	if !validStandardResourceRead(accountID, appID) || !validStandardResourceRead(depID, depID) {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	for _, p := range m.buildExportPublications {
		c := p.Input.Claims
		if c.AccountID == canonicalStandardUUID(accountID) && c.AppID == canonicalStandardUUID(appID) && c.DeploymentID == canonicalStandardUUID(depID) {
			return true, nil
		}
	}
	return false, nil
}

func (m *MemStore) RecordBuildExportPublication(ctx context.Context, input BuildExportPublicationInput) (BuildExportPublication, error) {
	in, hash, err := prepareBuildExportPublication(input)
	if err != nil {
		return BuildExportPublication{}, err
	}
	if err := ctx.Err(); err != nil {
		return BuildExportPublication{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return BuildExportPublication{}, err
	}
	if err := m.checkBuildExportPublicationLocked(in, false); err != nil {
		return BuildExportPublication{}, err
	}
	for _, old := range m.buildExportPublications {
		if old.Input.Claims.BuildID == in.Claims.BuildID && old.Input.Claims.ClaimStartedAt == in.Claims.ClaimStartedAt && old.Input.Claims != in.Claims {
			return BuildExportPublication{}, ErrConflict
		}
	}
	if old, ok := m.buildExportPublications[in.ID]; ok {
		if old.InputHash != hash {
			return BuildExportPublication{}, ErrConflict
		}
		old.Input.Proof = old.Input.Proof.Clone()
		return old, nil
	}
	if m.buildExportPublications == nil {
		m.buildExportPublications = map[string]BuildExportPublication{}
	}
	now := time.Now().UTC()
	value := BuildExportPublication{ID: in.ID, InputHash: hash, Input: in, VerifiedAt: now, ExpiresAt: now.Add(api.BuildExportPublicationVerificationTTL)}
	m.buildExportPublications[in.ID] = value
	value.Input.Proof = value.Input.Proof.Clone()
	return value, nil
}

func (m *MemStore) checkBuildExportPublicationLocked(in BuildExportPublicationInput, fresh bool) error {
	app, found, dep, exists := m.registryVerificationOwnerLocked(in.Claims.AppID, in.Claims.DeploymentID)
	var build Build
	for _, b := range m.builds {
		if sameStandardUUID(b.ID, in.Claims.BuildID) {
			build = b
			break
		}
	}
	if !found || !exists || build.ID == "" {
		return ErrNotFound
	}
	if err := checkBuildExportOwner(in.Claims, app, dep, build); err != nil {
		return err
	}
	if fresh || build.Status == BuildSucceeded {
		p := m.buildProvenance[build.ID]
		if build.Status != BuildSucceeded || p.SourceSHA256 != in.Claims.SourceSHA256 || p.BuilderNodeID != in.Claims.BuilderNodeID || !p.StartedAt.Equal(build.StartedAt) {
			return ErrApplicationStandardRuntimeStale
		}
	} else if build.Status != BuildRunning {
		return ErrApplicationStandardRuntimeStale
	}
	signer := m.trustedSigners[trustedSignerKey{AppID: app.ID, SignerName: in.Proof.PublisherName}]
	if !sameStandardUUID(signer.AccountID, app.AccountID) {
		return buildpublisher.ErrInvalid
	}
	return buildpublisher.Verify(in.Claims, in.Proof, signer.CosignPublicKey)
}

func (m *MemStore) GetFreshBuildExportPublication(ctx context.Context, accountID, appID, depID, buildID string) (BuildExportPublication, error) {
	if !validStandardResourceRead(accountID, appID) || !validStandardResourceRead(depID, buildID) {
		return BuildExportPublication{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return BuildExportPublication{}, err
	}
	var latest BuildExportPublication
	for _, p := range m.buildExportPublications {
		c := p.Input.Claims
		if c.AccountID == canonicalStandardUUID(accountID) && c.AppID == canonicalStandardUUID(appID) && c.DeploymentID == canonicalStandardUUID(depID) && c.BuildID == canonicalStandardUUID(buildID) && (latest.ID == "" || p.VerifiedAt.After(latest.VerifiedAt) || p.VerifiedAt.Equal(latest.VerifiedAt) && p.ID > latest.ID) {
			latest = p
		}
	}
	if latest.ID == "" {
		return latest, ErrNotFound
	}
	if err := validateBuildExportPublication(latest); err != nil {
		return BuildExportPublication{}, err
	}
	now := time.Now()
	if latest.VerifiedAt.After(now) || !latest.ExpiresAt.After(now) {
		return BuildExportPublication{}, ErrApplicationStandardRuntimeStale
	}
	if err := m.checkBuildExportPublicationLocked(latest.Input, true); err != nil {
		return BuildExportPublication{}, err
	}
	latest.Input.Proof = latest.Input.Proof.Clone()
	return latest, nil
}
