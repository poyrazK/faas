package state

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"time"
)

var _ DevSourcePatchStore = (*MemStore)(nil)

// RecordDevSourceManifest mirrors PgStore.RecordDevSourceManifest.
func (m *MemStore) RecordDevSourceManifest(_ context.Context, manifest DevSourceManifest, keep int) error {
	if manifest.DeploymentID == "" || manifest.AppID == "" {
		return errors.New("state: developer source manifest needs deployment and app ids")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.devSourceManifests == nil {
		m.devSourceManifests = map[string]DevSourceManifest{}
	}
	if existing, ok := m.devSourceManifests[manifest.DeploymentID]; ok {
		manifest.CreatedAt = existing.CreatedAt
	} else {
		m.devSourceManifestSeq++
		manifest.CreatedAt = time.Unix(0, m.devSourceManifestSeq).UTC()
	}
	manifest.Entries = copyDevSourceEntries(manifest.Entries)
	m.devSourceManifests[manifest.DeploymentID] = manifest
	var app []DevSourceManifest
	for _, candidate := range m.devSourceManifests {
		if candidate.AppID == manifest.AppID {
			app = append(app, candidate)
		}
	}
	sort.Slice(app, func(i, j int) bool { return app[i].CreatedAt.After(app[j].CreatedAt) })
	for i, candidate := range app {
		if i < keep || m.deployments[candidate.DeploymentID].Status == DeployLive {
			continue
		}
		delete(m.devSourceManifests, candidate.DeploymentID)
	}
	return nil
}

// DevSourceManifest mirrors PgStore.DevSourceManifest.
func (m *MemStore) DevSourceManifest(_ context.Context, deploymentID string) (DevSourceManifest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	manifest, ok := m.devSourceManifests[deploymentID]
	if !ok {
		return DevSourceManifest{}, ErrNotFound
	}
	manifest.Entries = copyDevSourceEntries(manifest.Entries)
	return manifest, nil
}

// CreateDevSourcePatch mirrors PgStore.CreateDevSourcePatch.
func (m *MemStore) CreateDevSourcePatch(_ context.Context, patch DevSourcePatch) (DevSourcePatch, error) {
	if patch.AppID == "" || patch.BaseDeploymentID == "" {
		return DevSourcePatch{}, errors.New("state: developer patch needs app and base deployment ids")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	kept := m.devSourcePatches[:0]
	var latest int64
	for _, existing := range m.devSourcePatches {
		if existing.AppID == patch.AppID && (!existing.ExpiresAt.After(now) || existing.BaseDeploymentID != patch.BaseDeploymentID) {
			continue
		}
		if existing.BaseDeploymentID == patch.BaseDeploymentID && existing.Generation > latest {
			latest = existing.Generation
		}
		kept = append(kept, existing)
	}
	m.devSourcePatchSeq++
	patch.ID = "dev-patch-" + strconv.FormatInt(m.devSourcePatchSeq, 10)
	patch.Generation = latest + 1
	patch.CreatedAt = now
	patch.Archive = append([]byte(nil), patch.Archive...)
	patch.Deleted = append([]string{}, patch.Deleted...)
	m.devSourcePatches = append(kept, patch)
	return patch, nil
}

// LatestDevSourcePatch mirrors PgStore.LatestDevSourcePatch.
func (m *MemStore) LatestDevSourcePatch(_ context.Context, appID, baseDeploymentID string, afterGeneration int64) (DevSourcePatch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var best *DevSourcePatch
	for i := range m.devSourcePatches {
		patch := &m.devSourcePatches[i]
		if patch.AppID != appID || patch.BaseDeploymentID != baseDeploymentID || patch.Generation <= afterGeneration || !patch.ExpiresAt.After(now) {
			continue
		}
		if best == nil || patch.Generation > best.Generation {
			best = patch
		}
	}
	if best == nil {
		return DevSourcePatch{}, ErrNotFound
	}
	out := *best
	out.Archive = append([]byte(nil), best.Archive...)
	out.Deleted = append([]string{}, best.Deleted...)
	return out, nil
}

func copyDevSourceEntries(entries map[string]DevSourceEntry) map[string]DevSourceEntry {
	out := make(map[string]DevSourceEntry, len(entries))
	for name, entry := range entries {
		out[name] = entry
	}
	return out
}

// RecordDevSourcePatchApplied mirrors PgStore.RecordDevSourcePatchApplied.
func (m *MemStore) RecordDevSourcePatchApplied(_ context.Context, appID, baseDeploymentID string, generation, applyMS int64, applyError string) (DevSourcePatchAck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.devSourcePatches {
		patch := &m.devSourcePatches[i]
		if patch.AppID == appID && patch.BaseDeploymentID == baseDeploymentID && patch.Generation == generation && patch.AppliedAt == nil {
			now := time.Now().UTC()
			patch.AppliedAt, patch.ApplyMS, patch.ApplyError = &now, applyMS, applyError
			return DevSourcePatchAck{First: true, Delivery: max(now.Sub(patch.CreatedAt), 0)}, nil
		}
	}
	return DevSourcePatchAck{}, nil
}

// DevSourcePatchStatus mirrors PgStore.DevSourcePatchStatus.
func (m *MemStore) DevSourcePatchStatus(_ context.Context, appID string, generation int64) (DevSourcePatchStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *DevSourcePatch
	for i := range m.devSourcePatches {
		patch := &m.devSourcePatches[i]
		if patch.AppID == appID && patch.Generation == generation && (best == nil || patch.CreatedAt.After(best.CreatedAt)) {
			best = patch
		}
	}
	if best == nil {
		return DevSourcePatchStatus{}, ErrNotFound
	}
	status := DevSourcePatchStatus{Generation: best.Generation, CreatedAt: best.CreatedAt, ApplyMS: best.ApplyMS, ApplyError: best.ApplyError}
	if best.AppliedAt != nil {
		appliedAt := *best.AppliedAt
		status.AppliedAt = &appliedAt
	}
	return status, nil
}
