package state

import (
	"context"
	"sort"
	"time"
)

var _ ObjectBucketAccessStore = (*MemStore)(nil)

func objectBucketAccessGrantKey(bucketID, keyID string) string {
	return bucketID + "\x00" + keyID
}

func apiKeyHasScope(key APIKey, want string) bool {
	for _, scope := range key.Scopes {
		if scope == want {
			return true
		}
	}
	return false
}

func keySupportsObjectBucketPermission(key APIKey, permission string) bool {
	if apiKeyHasScope(key, "admin") {
		return false
	}
	switch permission {
	case ObjectBucketPermissionRead:
		return apiKeyHasScope(key, "storage:read")
	case ObjectBucketPermissionWrite:
		return apiKeyHasScope(key, "storage:write")
	case ObjectBucketPermissionReadWrite:
		return apiKeyHasScope(key, "storage:read") && apiKeyHasScope(key, "storage:write")
	default:
		return false
	}
}

func (m *MemStore) ListObjectBucketAccessGrants(_ context.Context, accountID, bucketID string) ([]ObjectBucketAccessGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, ok := m.objectBuckets[bucketID]
	if !ok || bucket.AccountID != accountID || bucket.State == "deleted" {
		return nil, ErrNotFound
	}
	out := make([]ObjectBucketAccessGrant, 0)
	for _, grant := range m.objectAccessGrants {
		key, exists := m.keys[grant.APIKeyID]
		if grant.AccountID != accountID || grant.BucketID != bucketID || !exists || key.AccountID != accountID {
			continue
		}
		grant.KeyLabel, grant.KeyStatus = key.Label, key.Status
		out = append(out, grant)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].APIKeyID < out[j].APIKeyID
	})
	return out, nil
}

func (m *MemStore) SetObjectBucketAccessGrant(_ context.Context, accountID, bucketID, keyID, permission string) (ObjectBucketAccessGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, bucketOK := m.objectBuckets[bucketID]
	key, keyOK := m.keys[keyID]
	if !bucketOK || bucket.AccountID != accountID || bucket.State == "deleted" || !keyOK || key.AccountID != accountID {
		return ObjectBucketAccessGrant{}, ErrNotFound
	}
	if (key.Status != string(APIKeyStatusActive) && key.Status != string(APIKeyStatusGrace)) || !keySupportsObjectBucketPermission(key, permission) {
		return ObjectBucketAccessGrant{}, ErrConflict
	}
	now := time.Now().UTC()
	mapKey := objectBucketAccessGrantKey(bucketID, keyID)
	grant, exists := m.objectAccessGrants[mapKey]
	if !exists {
		grant = ObjectBucketAccessGrant{AccountID: accountID, BucketID: bucketID, APIKeyID: keyID, CreatedAt: now}
	}
	grant.Permission, grant.UpdatedAt = permission, now
	grant.KeyLabel, grant.KeyStatus = key.Label, key.Status
	m.objectAccessGrants[mapKey] = grant
	return grant, nil
}

func (m *MemStore) DeleteObjectBucketAccessGrant(_ context.Context, accountID, bucketID, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mapKey := objectBucketAccessGrantKey(bucketID, keyID)
	grant, ok := m.objectAccessGrants[mapKey]
	if !ok || grant.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.objectAccessGrants, mapKey)
	return nil
}

func (m *MemStore) ObjectBucketKeyCan(_ context.Context, accountID, bucketID, keyID, permission string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if permission != ObjectBucketPermissionRead && permission != ObjectBucketPermissionWrite {
		return false, ErrConflict
	}
	bucket, bucketOK := m.objectBuckets[bucketID]
	key, keyOK := m.keys[keyID]
	grant, grantOK := m.objectAccessGrants[objectBucketAccessGrantKey(bucketID, keyID)]
	if !bucketOK || bucket.AccountID != accountID || bucket.State == "deleted" || !keyOK || key.AccountID != accountID || !grantOK || grant.AccountID != accountID {
		return false, nil
	}
	if key.Status != string(APIKeyStatusActive) && key.Status != string(APIKeyStatusGrace) {
		return false, nil
	}
	if permission == ObjectBucketPermissionRead {
		if !apiKeyHasScope(key, "storage:read") {
			return false, nil
		}
		return grant.Permission == ObjectBucketPermissionRead || grant.Permission == ObjectBucketPermissionReadWrite, nil
	}
	if !apiKeyHasScope(key, "storage:write") {
		return false, nil
	}
	return grant.Permission == ObjectBucketPermissionWrite || grant.Permission == ObjectBucketPermissionReadWrite, nil
}

func (m *MemStore) ListObjectBucketsForKey(_ context.Context, accountID, appID, keyID string) ([]ObjectBucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := m.keys[keyID]
	if !ok || key.AccountID != accountID || (key.Status != string(APIKeyStatusActive) && key.Status != string(APIKeyStatusGrace)) {
		return []ObjectBucket{}, nil
	}
	out := make([]ObjectBucket, 0)
	for _, bucket := range m.objectBuckets {
		grant, granted := m.objectAccessGrants[objectBucketAccessGrantKey(bucket.ID, keyID)]
		if bucket.AccountID == accountID && bucket.AppID == appID && bucket.State != "deleted" && granted && grant.AccountID == accountID {
			out = append(out, bucket)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemStore) copyObjectBucketAccessGrantsLocked(oldKeyID, newKeyID string) {
	for _, old := range m.objectAccessGrants {
		if old.APIKeyID != oldKeyID {
			continue
		}
		now := time.Now().UTC()
		copy := old
		copy.APIKeyID, copy.CreatedAt, copy.UpdatedAt = newKeyID, now, now
		if key, ok := m.keys[newKeyID]; ok {
			copy.KeyLabel, copy.KeyStatus = key.Label, key.Status
		}
		m.objectAccessGrants[objectBucketAccessGrantKey(copy.BucketID, newKeyID)] = copy
	}
}

func (m *MemStore) deleteObjectBucketAccessGrantsForKeyLocked(keyID string) {
	for mapKey, grant := range m.objectAccessGrants {
		if grant.APIKeyID == keyID {
			delete(m.objectAccessGrants, mapKey)
		}
	}
}
