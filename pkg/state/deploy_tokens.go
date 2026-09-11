package state

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DeployTokenStore is the optional store surface used by the per-app deploy
// token handlers. It intentionally stays outside Store so existing test seams
// and downstream store implementations do not need a flag-day interface bump.
type DeployTokenStore interface {
	CreateDeployToken(ctx context.Context, accountID, appID string, hash []byte, label string, scopes []string, expiresAt time.Time) (DeployToken, error)
	ListDeployTokensForApp(ctx context.Context, accountID, appID string) ([]DeployToken, error)
	GetDeployToken(ctx context.Context, accountID, appID, tokenID string) (DeployToken, error)
	RevokeDeployToken(ctx context.Context, accountID, appID, tokenID string) (DeployToken, error)
	RotateDeployToken(ctx context.Context, accountID, appID, tokenID string, hash []byte, label string, expiresAt time.Time, graceWindow time.Duration) (newToken, oldToken DeployToken, err error)
	AuthenticateDeployToken(ctx context.Context, hash []byte) (Account, APIKey, error)
	TouchDeployTokenLastUsed(ctx context.Context, tokenID string) error
}

func (m *MemStore) CreateDeployToken(_ context.Context, accountID, appID string, hash []byte, label string, scopes []string, expiresAt time.Time) (DeployToken, error) {
	if accountID == "" || appID == "" {
		return DeployToken{}, errors.New("state: deploy token requires account_id and app_id")
	}
	if len(hash) != 32 {
		return DeployToken{}, fmt.Errorf("state: deploy token hash must be 32 bytes, got %d", len(hash))
	}
	if expiresAt.IsZero() || !expiresAt.After(time.Now()) {
		return DeployToken{}, errors.New("state: deploy token expiry must be in the future")
	}
	if len(scopes) == 0 {
		scopes = []string{"deploy:write"}
	}
	for _, scope := range scopes {
		if scope != "deploy:write" {
			return DeployToken{}, fmt.Errorf("state: unsupported deploy token scope %q", scope)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID {
		return DeployToken{}, ErrNotFound
	}
	hashKey := string(hash)
	if _, ok := m.deployTokenByHash[hashKey]; ok {
		return DeployToken{}, ErrConflict
	}
	t := DeployToken{ID: newID(), AccountID: accountID, AppID: appID,
		Hash: append([]byte(nil), hash...), Label: label,
		Scopes: append([]string(nil), scopes...), Status: string(APIKeyStatusActive),
		CreatedAt: time.Now().UTC(), ExpiresAt: deployTokenTimePtr(expiresAt.UTC())}
	m.deployTokens[t.ID] = t
	m.deployTokenByHash[hashKey] = t
	return t, nil
}

func (m *MemStore) ListDeployTokensForApp(_ context.Context, accountID, appID string) ([]DeployToken, error) {
	if accountID == "" || appID == "" {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DeployToken
	for _, t := range m.deployTokens {
		if t.AccountID == accountID && t.AppID == appID {
			out = append(out, cloneDeployToken(t))
		}
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].CreatedAt.After(out[i].CreatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

func (m *MemStore) GetDeployToken(_ context.Context, accountID, appID, tokenID string) (DeployToken, error) {
	if accountID == "" || appID == "" || tokenID == "" {
		return DeployToken{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.deployTokens[tokenID]
	if !ok || t.AccountID != accountID || t.AppID != appID {
		return DeployToken{}, ErrNotFound
	}
	return cloneDeployToken(t), nil
}

func (m *MemStore) RevokeDeployToken(_ context.Context, accountID, appID, tokenID string) (DeployToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.deployTokens[tokenID]
	if !ok || t.AccountID != accountID || t.AppID != appID {
		return DeployToken{}, ErrNotFound
	}
	if t.RevokedAt == nil {
		now := time.Now().UTC()
		t.RevokedAt = &now
		t.Status = string(APIKeyStatusRevoked)
		m.deployTokens[tokenID] = t
		m.deployTokenByHash[string(t.Hash)] = t
	}
	return cloneDeployToken(t), nil
}

func (m *MemStore) RotateDeployToken(_ context.Context, accountID, appID, tokenID string, hash []byte, label string, expiresAt time.Time, graceWindow time.Duration) (DeployToken, DeployToken, error) {
	if len(hash) != 32 || !expiresAt.After(time.Now()) {
		return DeployToken{}, DeployToken{}, errors.New("state: invalid rotated deploy token")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.deployTokens[tokenID]
	if !ok || old.AccountID != accountID || old.AppID != appID {
		return DeployToken{}, DeployToken{}, ErrNotFound
	}
	if old.Status == string(APIKeyStatusRevoked) {
		return DeployToken{}, DeployToken{}, ErrAPIKeyRevoked
	}
	if old.ExpiresAt != nil && !old.ExpiresAt.After(time.Now()) {
		return DeployToken{}, DeployToken{}, ErrAPIKeyExpired
	}
	if _, exists := m.deployTokenByHash[string(hash)]; exists {
		return DeployToken{}, DeployToken{}, ErrConflict
	}
	if label == "" {
		label = old.Label
	}
	rotatedFrom := old.ID
	newToken := DeployToken{ID: newID(), AccountID: accountID, AppID: appID,
		Hash: append([]byte(nil), hash...), Label: label,
		Scopes: append([]string(nil), old.Scopes...), Status: string(APIKeyStatusActive),
		CreatedAt: time.Now().UTC(), ExpiresAt: deployTokenTimePtr(expiresAt.UTC()), RotatedFromID: &rotatedFrom}
	m.deployTokens[newToken.ID] = newToken
	m.deployTokenByHash[string(newToken.Hash)] = newToken
	if graceWindow <= 0 {
		now := time.Now().UTC()
		old.Status = string(APIKeyStatusRevoked)
		old.RevokedAt = &now
	} else {
		old.Status = string(APIKeyStatusGrace)
		grace := time.Now().UTC().Add(graceWindow)
		old.ExpiresAt = &grace
	}
	m.deployTokens[old.ID] = old
	m.deployTokenByHash[string(old.Hash)] = old
	return cloneDeployToken(newToken), cloneDeployToken(old), nil
}

func (m *MemStore) AuthenticateDeployToken(_ context.Context, hash []byte) (Account, APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.deployTokenByHash[string(hash)]
	if !ok {
		return Account{}, APIKey{}, ErrNotFound
	}
	acct, ok := m.accounts[t.AccountID]
	if !ok {
		return Account{}, APIKey{}, ErrNotFound
	}
	if t.Status == string(APIKeyStatusRevoked) {
		return Account{}, APIKey{}, ErrAPIKeyRevoked
	}
	if t.ExpiresAt == nil || !t.ExpiresAt.After(time.Now()) {
		now := time.Now().UTC()
		t.Status = string(APIKeyStatusRevoked)
		t.RevokedAt = &now
		m.deployTokens[t.ID] = t
		m.deployTokenByHash[string(hash)] = t
		return Account{}, APIKey{}, ErrAPIKeyExpired
	}
	return acct, APIKey{ID: t.ID, AccountID: t.AccountID, AppID: t.AppID,
		Hash: append([]byte(nil), t.Hash...), Label: t.Label,
		Scopes: append([]string(nil), t.Scopes...), CreatedAt: t.CreatedAt,
		ExpiresAt: t.ExpiresAt, Status: t.Status}, nil
}

func (m *MemStore) TouchDeployTokenLastUsed(_ context.Context, tokenID string) error {
	if tokenID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.deployTokens[tokenID]
	if !ok || t.Status == string(APIKeyStatusRevoked) {
		return nil
	}
	now := time.Now().UTC()
	t.LastUsedAt = &now
	m.deployTokens[tokenID] = t
	m.deployTokenByHash[string(t.Hash)] = t
	return nil
}

func cloneDeployToken(t DeployToken) DeployToken {
	t.Hash = append([]byte(nil), t.Hash...)
	t.Scopes = append([]string(nil), t.Scopes...)
	return t
}

func deployTokenTimePtr(t time.Time) *time.Time { return &t }
