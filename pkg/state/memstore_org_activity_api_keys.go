package state

import (
	"context"
	"encoding/hex"
	"errors"
	"time"
)

var _ OrgActivityAPIKeyMutationStore = (*MemStore)(nil)

func (m *MemStore) CreateOrgAPIKeyWithActivity(_ context.Context, orgID, accountID string, hash []byte, label string, scopes []string, expiresAt *time.Time, createdIP, createdUA string, parent *string, activity OrgActivity) (APIKey, int64, error) {
	activity, err := normalizeOrgActivity(activity, time.Now())
	if err != nil {
		return APIKey{}, 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	hashKey := hex.EncodeToString(hash)
	if _, exists := m.keyByHash[hashKey]; exists {
		return APIKey{}, 0, errors.New("state: duplicate key hash")
	}
	if err := requireAPIKeyScopes(scopes); err != nil {
		return APIKey{}, 0, err
	}
	key := APIKey{
		ID: newID(), AccountID: accountID, OrgID: orgID,
		RunsPrincipalID: newID(),
		Hash:            append([]byte(nil), hash...), Label: label, Scopes: append([]string(nil), scopes...),
		CreatedAt: time.Now(), Status: string(APIKeyStatusActive), ExpiresAt: expiresAt,
		CreatedIP: createdIP, CreatedUA: createdUA, ParentKeyID: parent,
	}
	activity, err = bindOrgActivityToAPIKey(activity, key)
	if err != nil {
		return APIKey{}, 0, err
	}
	m.keys[key.ID] = key
	m.keyByHash[hashKey] = key
	return key, m.enqueueOrgActivityOutboxLocked(activity), nil
}

func (m *MemStore) RevokeOrgAPIKeyWithActivity(_ context.Context, orgID, keyID string, activity OrgActivity) (APIKey, int64, error) {
	activity, err := normalizeOrgActivity(activity, time.Now())
	if err != nil {
		return APIKey{}, 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	key, ok := m.keys[keyID]
	if !ok || key.OrgID != orgID {
		return APIKey{}, 0, ErrNotFound
	}
	if key.Status == string(APIKeyStatusRevoked) {
		return key, 0, nil
	}
	now := time.Now()
	key.Status = string(APIKeyStatusRevoked)
	if key.RevokedAt == nil {
		key.RevokedAt = &now
	}
	activity, err = bindOrgActivityToAPIKey(activity, key)
	if err != nil {
		return APIKey{}, 0, err
	}
	m.keys[key.ID] = key
	m.keyByHash[hex.EncodeToString(key.Hash)] = key
	return key, m.enqueueOrgActivityOutboxLocked(activity), nil
}

func (m *MemStore) RotateOrgAPIKeyWithActivity(_ context.Context, orgID, oldKeyID string, newHash []byte, newLabel string, graceWindow time.Duration, createdIP, createdUA string, parent *string, activity OrgActivity) (APIKey, APIKey, int64, error) {
	activity, err := normalizeOrgActivity(activity, time.Now())
	if err != nil {
		return APIKey{}, APIKey{}, 0, err
	}
	if graceWindow < 0 {
		graceWindow = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	old, ok := m.keys[oldKeyID]
	if !ok || old.OrgID != orgID {
		return APIKey{}, APIKey{}, 0, ErrNotFound
	}
	if old.Status == string(APIKeyStatusRevoked) {
		return APIKey{}, APIKey{}, 0, ErrAPIKeyRevoked
	}
	if _, exists := m.keyByHash[hex.EncodeToString(newHash)]; exists {
		return APIKey{}, APIKey{}, 0, errors.New("state: duplicate key hash")
	}
	if newLabel == "" {
		newLabel = old.Label
	}
	rotatedFrom := old.ID
	newKey := APIKey{
		ID: newID(), AccountID: old.AccountID, OrgID: old.OrgID,
		RunsPrincipalID: old.RunsPrincipalID,
		Hash:            append([]byte(nil), newHash...), Label: newLabel, Scopes: append([]string(nil), old.Scopes...),
		CreatedAt: time.Now(), Status: string(APIKeyStatusActive), RotatedFromID: &rotatedFrom,
		CreatedIP: createdIP, CreatedUA: createdUA, ParentKeyID: parent,
	}
	activity, err = bindOrgActivityToAPIKey(activity, newKey)
	if err != nil {
		return APIKey{}, APIKey{}, 0, err
	}

	now := time.Now()
	if graceWindow == 0 {
		old.Status = string(APIKeyStatusRevoked)
		old.ExpiresAt = &now
		if old.RevokedAt == nil {
			old.RevokedAt = &now
		}
	} else {
		old.Status = string(APIKeyStatusGrace)
		deadline := now.Add(graceWindow)
		old.ExpiresAt = &deadline
	}
	m.keys[newKey.ID] = newKey
	m.keyByHash[hex.EncodeToString(newKey.Hash)] = newKey
	m.copyObjectBucketAccessGrantsLocked(old.ID, newKey.ID)
	m.keys[old.ID] = old
	m.keyByHash[hex.EncodeToString(old.Hash)] = old
	return newKey, old, m.enqueueOrgActivityOutboxLocked(activity), nil
}
