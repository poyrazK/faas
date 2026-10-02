package state

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/region"
)

func (m *MemStore) RuntimeLogArchiveDayExpired(_ context.Context, instance, day string) (bool, error) {
	if !region.ValidRuntimeLogDay(day) {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return day < m.runtimeLogArchiveExpiry[instance], nil
}

func (m *MemStore) ListRuntimeLogArchiveRetentionCandidates(_ context.Context, q RuntimeLogArchiveRetentionQuery) ([]RuntimeLogArchive, error) {
	if !q.valid() {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RuntimeLogArchive
	for _, ref := range m.runtimeLogArchives {
		if ref.Day < q.BeforeDay && (ref.Day > q.AfterDay || (ref.Day == q.AfterDay && ref.InstanceID > q.AfterInstance)) {
			out = append(out, ref)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Day < out[j].Day || (out[i].Day == out[j].Day && out[i].InstanceID < out[j].InstanceID)
	})
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (m *MemStore) RetireRuntimeLogArchive(_ context.Context, ref RuntimeLogArchive) error {
	if !ref.ValidReference() || ref.Day == "9999-12-31" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	binding, exists := m.runtimeLogArchiveBindings[ref.InstanceID]
	if !exists {
		return ErrNotFound
	}
	if binding != ref.RuntimeLogArchiveBinding {
		return ErrInvalidArgument
	}
	actual, ok := m.runtimeLogArchives[archiveMapKey(ref.InstanceID, ref.Day)]
	if !ok {
		return ErrNotFound
	}
	if actual.StorageKey != ref.StorageKey {
		return ErrInvalidArgument
	}
	day, _ := time.Parse("2006-01-02", ref.Day)
	floor := day.AddDate(0, 0, 1).Format("2006-01-02")
	if floor > m.runtimeLogArchiveExpiry[ref.InstanceID] {
		if m.runtimeLogArchiveExpiry == nil {
			m.runtimeLogArchiveExpiry = make(map[string]string)
		}
		m.runtimeLogArchiveExpiry[ref.InstanceID] = floor
	}
	return nil
}

func (m *MemStore) DeleteCollectedRuntimeLogArchive(_ context.Context, ref RuntimeLogArchive) error {
	if !ref.ValidReference() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	binding, exists := m.runtimeLogArchiveBindings[ref.InstanceID]
	if !exists {
		return ErrNotFound
	}
	if binding != ref.RuntimeLogArchiveBinding {
		return ErrInvalidArgument
	}
	key := archiveMapKey(ref.InstanceID, ref.Day)
	actual, ok := m.runtimeLogArchives[key]
	if !ok || ref.Day >= m.runtimeLogArchiveExpiry[ref.InstanceID] {
		return ErrNotFound
	}
	if actual.StorageKey != ref.StorageKey {
		return ErrInvalidArgument
	}
	delete(m.runtimeLogArchives, key)
	for fragment := range m.runtimeLogArchiveFragments {
		if strings.HasPrefix(fragment, key+"\x00") {
			delete(m.runtimeLogArchiveFragments, fragment)
		}
	}
	return nil
}
