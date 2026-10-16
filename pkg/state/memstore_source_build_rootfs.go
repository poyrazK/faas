package state

// adr: 435. Approval, intent, base selection and rootfs metadata share one lock.

import (
	"context"
	"time"
)

var _ SourceBuildRootfsStore = (*MemStore)(nil)

func (m *MemStore) PublishSourceBuildRootfs(ctx context.Context, input SourceBuildRootfsInput) (SourceBuildRootfs, error) {
	in, hash, err := prepareSourceBuildRootfs(input)
	if err != nil {
		return SourceBuildRootfs{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SourceBuildRootfs{}, err
	}
	return m.publishSourceBuildRootfsLocked(in, hash)
}

func (m *MemStore) publishSourceBuildRootfsLocked(in SourceBuildRootfsInput, hash string) (SourceBuildRootfs, error) {
	dep, parent, err := m.sourceBuildRootfsParentsLocked(in, time.Now().UTC())
	if err != nil {
		return SourceBuildRootfs{}, err
	}
	if old, ok := m.sourceBuildRootfs[in.ID]; ok {
		if old.InputHash != hash || m.sourceBuildRootfsCurrent[in.DeploymentID] != in.ID {
			return SourceBuildRootfs{}, ErrConflict
		}
		if !sourceBuildRootfsMetadataMatches(old, dep) {
			return SourceBuildRootfs{}, ErrApplicationStandardRuntimeStale
		}
		return old, nil
	}
	now := time.Now().UTC()
	if !parent.ExpiresAt.After(now) {
		return SourceBuildRootfs{}, ErrApplicationStandardRuntimeStale
	}
	value := SourceBuildRootfs{ID: in.ID, InputHash: hash, Input: in, PublishedAt: now, ExpiresAt: parent.ExpiresAt}
	if m.sourceBuildRootfs == nil {
		m.sourceBuildRootfs = map[string]SourceBuildRootfs{}
	}
	if m.sourceBuildRootfsCurrent == nil {
		m.sourceBuildRootfsCurrent = map[string]string{}
	}
	dep.RootfsPath, dep.RootfsKey, dep.RootfsBytes = in.RootfsPath, in.StorageKey, in.ContentBytes
	m.deployments[dep.ID] = dep
	m.sourceBuildRootfs[in.ID] = value
	m.sourceBuildRootfsCurrent[in.DeploymentID] = in.ID
	return value, nil
}

func (m *MemStore) sourceBuildRootfsParentsLocked(in SourceBuildRootfsInput, now time.Time) (Deployment, BuildExportPublication, error) {
	app, found, dep, exists := m.registryVerificationOwnerLocked(in.AppID, in.DeploymentID)
	if !found || !exists {
		return dep, BuildExportPublication{}, ErrNotFound
	}
	parent, ok := m.buildExportPublications[in.PublicationID]
	if !ok {
		return dep, parent, ErrNotFound
	}
	if err := checkSourceBuildRootfsParent(in, parent, app, dep, now); err != nil {
		return dep, parent, err
	}
	if err := m.checkBuildExportPublicationLocked(parent.Input, true); err != nil {
		return dep, parent, err
	}
	if latest := m.latestSourceBuildLocked(dep.ID); !sameStandardUUID(latest.ID, parent.Input.Claims.BuildID) || !sameStandardUUID(dep.BuildID, parent.Input.Claims.BuildID) {
		return dep, parent, ErrApplicationStandardRuntimeStale
	}
	base, ok := m.baseImageProducers[in.BaseProducerID]
	if !ok {
		return dep, parent, ErrNotFound
	}
	if base.PublishedAt.After(now) || m.baseImageProducerCurrent[base.Input.Artifact.StorageKey] != base.ID {
		return dep, parent, ErrApplicationStandardRuntimeStale
	}
	if err := checkSourceBuildRootfsBase(in, base); err != nil {
		return dep, parent, err
	}
	return dep, parent, nil
}

func (m *MemStore) latestSourceBuildLocked(depID string) Build {
	var latest Build
	for _, b := range m.builds {
		if sameStandardUUID(b.DeploymentID, depID) && (latest.ID == "" || b.StartedAt.After(latest.StartedAt) || b.StartedAt.Equal(latest.StartedAt) && canonicalStandardUUID(b.ID) > canonicalStandardUUID(latest.ID)) {
			latest = b
		}
	}
	return latest
}

func (m *MemStore) GetCurrentSourceBuildRootfs(ctx context.Context, accountID, appID, depID string) (SourceBuildRootfs, error) {
	if !validStandardResourceRead(accountID, appID) || !validStandardResourceRead(depID, depID) {
		return SourceBuildRootfs{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SourceBuildRootfs{}, err
	}
	app, found, dep, exists := m.registryVerificationOwnerLocked(appID, depID)
	if !found || !exists || app.Status == AppDeleted || !sameStandardUUID(app.AccountID, accountID) {
		return SourceBuildRootfs{}, ErrNotFound
	}
	value, ok := m.sourceBuildRootfs[m.sourceBuildRootfsCurrent[canonicalStandardUUID(depID)]]
	if !ok || value.Input.AccountID != canonicalStandardUUID(accountID) || value.Input.AppID != canonicalStandardUUID(appID) ||
		value.Input.OrgID != registryCanonicalOrg(app.OrgID) || !sourceBuildRootfsMetadataMatches(value, dep) {
		return SourceBuildRootfs{}, ErrNotFound
	}
	parent, ok := m.buildExportPublications[value.Input.PublicationID]
	if !ok || !sourceBuildRootfsHistoricalParent(value.Input, parent, app, dep) {
		return SourceBuildRootfs{}, ErrNotFound
	}
	if err := validateSourceBuildRootfs(value); err != nil {
		return SourceBuildRootfs{}, err
	}
	return value, nil
}

func sourceBuildRootfsHistoricalParent(in SourceBuildRootfsInput, parent BuildExportPublication, app App, dep Deployment) bool {
	c := parent.Input.Claims
	if validateBuildExportPublication(parent) != nil || in.PublicationHash != parent.InputHash ||
		c.AccountID != canonicalStandardUUID(app.AccountID) || c.AppID != canonicalStandardUUID(app.ID) ||
		c.OrgID != registryCanonicalOrg(app.OrgID) || c.DeploymentID != canonicalStandardUUID(dep.ID) || c.Runtime != app.Runtime ||
		dep.SourceSHA256 != "" && c.SourceSHA256 != dep.SourceSHA256 {
		return false
	}
	switch dep.Kind {
	case DeploymentKindTarball, DeploymentKindDockerfile, DeploymentKindGitHub, DeploymentKindPreview:
		return true
	default:
		return false
	}
}

func (m *MemStore) deleteAppSourceBuildRootfsLocked(appID string) {
	for id, value := range m.sourceBuildRootfs {
		if sameStandardUUID(value.Input.AppID, appID) {
			delete(m.sourceBuildRootfs, id)
			delete(m.sourceBuildRootfsCurrent, value.Input.DeploymentID)
		}
	}
}
