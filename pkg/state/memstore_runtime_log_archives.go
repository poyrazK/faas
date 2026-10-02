package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/region"
	"sort"
	"strings"
	"time"
)

func archiveMapKey(instance, day string) string { return instance + "\x00" + day }

func (m *MemStore) CaptureRuntimeLogArchiveBinding(_ context.Context, instanceID, nodeID string) (RuntimeLogArchiveBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.instances[instanceID]
	if !ok || nodeID == "" || i.NodeID != nodeID {
		return RuntimeLogArchiveBinding{}, ErrNotFound
	}
	d, ok := m.deployments[i.DeploymentID]
	if !ok || d.AppID != i.AppID {
		return RuntimeLogArchiveBinding{}, ErrNotFound
	}
	a, ok := m.apps[d.AppID]
	if !ok || a.Status != AppActive || m.accounts[a.AccountID].Status != AccountActive {
		return RuntimeLogArchiveBinding{}, ErrNotFound
	}
	b := RuntimeLogArchiveBinding{InstanceID: i.ID, AppID: a.ID, AccountID: a.AccountID, DeploymentID: d.ID, ComputeRegion: a.ComputeRegion}
	if old, ok := m.runtimeLogArchiveBindings[instanceID]; ok && old != b {
		return RuntimeLogArchiveBinding{}, ErrConflict
	}
	if m.runtimeLogArchiveBindings == nil {
		m.runtimeLogArchiveBindings = make(map[string]RuntimeLogArchiveBinding)
	}
	m.runtimeLogArchiveBindings[instanceID] = b
	return b, nil
}
func (m *MemStore) RuntimeLogArchiveBindingByInstance(_ context.Context, instanceID string) (RuntimeLogArchiveBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.runtimeLogArchiveBindings[instanceID]
	if !ok {
		return b, ErrNotFound
	}
	return b, nil
}
func (m *MemStore) ReserveRuntimeLogArchive(_ context.Context, a RuntimeLogArchive) error {
	if !a.ValidReference() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.runtimeLogArchiveBindings[a.InstanceID]
	if !ok || m.apps[b.AppID].Status != AppActive || m.accounts[b.AccountID].Status != AccountActive {
		return ErrNotFound
	}
	if b != a.RuntimeLogArchiveBinding {
		return ErrInvalidArgument
	}
	if a.Day < m.runtimeLogArchiveExpiry[a.InstanceID] {
		return ErrRuntimeLogArchiveExpired
	}
	k := archiveMapKey(a.InstanceID, a.Day)
	if old, ok := m.runtimeLogArchives[k]; ok {
		if old.StorageKey != a.StorageKey {
			return ErrInvalidArgument
		}
		return nil
	}
	if m.runtimeLogArchives == nil {
		m.runtimeLogArchives = make(map[string]RuntimeLogArchive)
	}
	a.Bytes, a.SHA256, a.PublishedAt = 0, "", time.Time{}
	m.runtimeLogArchives[k] = a
	return nil
}
func (m *MemStore) PublishRuntimeLogArchive(ctx context.Context, instanceID, day string, bytes int64, digest string) error {
	return m.PublishRuntimeLogArchiveFragment(ctx, instanceID, day, bytes, digest, "")
}
func (m *MemStore) PublishRuntimeLogArchiveFragment(_ context.Context, instanceID, day string, bytes int64, digest, fragment string) error {
	if !region.ValidRuntimeLogDay(day) || bytes <= 0 || !region.ValidArtifactSHA256(digest) || (fragment != "" && !region.ValidArtifactSHA256(fragment)) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if day < m.runtimeLogArchiveExpiry[instanceID] {
		return ErrRuntimeLogArchiveExpired
	}
	k := archiveMapKey(instanceID, day)
	a, ok := m.runtimeLogArchives[k]
	if !ok || m.apps[a.AppID].Status != AppActive || m.accounts[a.AccountID].Status != AccountActive {
		return ErrNotFound
	}
	a.Bytes, a.SHA256, a.PublishedAt = bytes, digest, time.Now()
	m.runtimeLogArchives[k] = a
	if fragment != "" {
		if m.runtimeLogArchiveFragments == nil {
			m.runtimeLogArchiveFragments = make(map[string]bool)
		}
		m.runtimeLogArchiveFragments[k+"\x00"+fragment] = true
	}
	return nil
}
func (m *MemStore) RuntimeLogArchiveFragmentPublished(_ context.Context, instance, day, fragment string) (bool, error) {
	if !region.ValidRuntimeLogDay(day) || !region.ValidArtifactSHA256(fragment) {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runtimeLogArchiveFragments[archiveMapKey(instance, day)+"\x00"+fragment], nil
}
func (m *MemStore) RuntimeLogArchiveByInstanceDay(_ context.Context, instanceID, day string) (RuntimeLogArchive, error) {
	if !region.ValidRuntimeLogDay(day) {
		return RuntimeLogArchive{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if day < m.runtimeLogArchiveExpiry[instanceID] {
		return RuntimeLogArchive{}, ErrRuntimeLogArchiveExpired
	}
	a, ok := m.runtimeLogArchives[archiveMapKey(instanceID, day)]
	if !ok {
		return a, ErrNotFound
	}
	return a, nil
}
func (m *MemStore) ListAppRuntimeLogArchives(_ context.Context, appID string) ([]RuntimeLogArchive, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RuntimeLogArchive, 0)
	for _, a := range m.runtimeLogArchives {
		if a.AppID == appID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StorageKey < out[j].StorageKey })
	return out, nil
}
func (m *MemStore) dropRuntimeLogArchivesForAppLocked(appID string) {
	for key, a := range m.runtimeLogArchives {
		if a.AppID == appID {
			delete(m.runtimeLogArchives, key)
			for fragment := range m.runtimeLogArchiveFragments {
				if strings.HasPrefix(fragment, key+"\x00") {
					delete(m.runtimeLogArchiveFragments, fragment)
				}
			}
		}
	}
	for key, b := range m.runtimeLogArchiveBindings {
		if b.AppID == appID {
			delete(m.runtimeLogArchiveBindings, key)
			delete(m.runtimeLogArchiveExpiry, key)
		}
	}
}
