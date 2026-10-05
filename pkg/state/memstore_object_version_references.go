package state

import (
	"context"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectVersionReferenceStore = (*MemStore)(nil)

func (m *MemStore) RecordObjectVersions(_ context.Context, account, bucket string, items []ObjectVersionIdentity) ([]ObjectVersionIdentity, error) {
	if !validVersionReferences(items) {
		return nil, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objectBuckets[bucket]
	if !ok || b.AccountID != account || b.State != "ready" {
		return nil, ErrNotFound
	}
	return m.recordObjectVersionsLocked(bucket, items), nil
}

func (m *MemStore) recordObjectVersionsLocked(bucket string, items []ObjectVersionIdentity) []ObjectVersionIdentity {
	out := make([]ObjectVersionIdentity, 0, len(items))
	for _, v := range items {
		v = m.prepareObjectVersionLocked(bucket, v)
		m.commitObjectVersionLocked(bucket, v)
		v.ID = publicPreparedVersionID(v)
		out = append(out, v)
	}
	return out
}

// Prepare without mutating maps so publication failure can leave the owning
// journal, references and accounting unchanged under the same MemStore lock.
func (m *MemStore) prepareObjectVersionLocked(bucket string, v ObjectVersionIdentity) ObjectVersionIdentity {
	if old, exists := m.objectVersionReferences[versionReferenceIdentity(bucket, v)]; exists {
		v.ID = old.ID
	} else {
		v.ID = uuid.NewString()
	}
	return v
}

func publicPreparedVersionID(v ObjectVersionIdentity) string {
	if v.ProviderVersionID == "null" {
		return "null"
	}
	return v.ID
}

func (m *MemStore) commitObjectVersionLocked(bucket string, v ObjectVersionIdentity) {
	if m.objectVersionReferences == nil {
		m.objectVersionReferences = map[string]ObjectVersionIdentity{}
		m.objectVersionReferenceIDs = map[string]string{}
		m.objectVersionObservations = map[string]bool{}
	}
	identity := versionReferenceIdentity(bucket, v)
	m.objectVersionReferences[identity] = v
	m.objectVersionReferenceIDs[v.ID] = identity
	m.objectVersionObservations[bucket] = m.objectVersionObservations[bucket] || v.ProviderVersionID != "null" || v.DeleteMarker
}

func (m *MemStore) ResolveObjectVersion(_ context.Context, account, bucket, key, id string) (string, error) {
	if !ValidObjectVersionID(id) || !validVersionReferenceText(key, api.MaxObjectS3ListTextBytes) {
		return "", ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objectBuckets[bucket]
	if !ok || b.AccountID != account || b.State != "ready" {
		return "", ErrNotFound
	}
	if id == "null" {
		return "null", nil
	}
	if identity, ok := m.objectVersionReferenceIDs[id]; ok {
		v := m.objectVersionReferences[identity]
		if v.Key == key && v.ProviderVersionID != "null" && identity == versionReferenceIdentity(bucket, v) {
			return v.ProviderVersionID, nil
		}
	}
	return "", ErrNotFound
}
