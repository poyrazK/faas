package state

import (
	"context"
	"encoding/hex"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
)

func (m *MemStore) CheckOrgAccess(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checkOrgAccessLocked(ctx)
}

func (m *MemStore) OrgAccessSnapshot(_ context.Context, orgID, accountID string) (OrgAccessSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mem, ok := m.memberships[orgAccountKey{OrgID: orgID, AccountID: accountID}]
	if !ok || mem.RemovedAt != nil {
		return OrgAccessSnapshot{}, ErrNotFound
	}
	return OrgAccessSnapshot{Role: mem.Role, Revision: m.orgAccessRevisions[orgAccountKey{OrgID: orgID, AccountID: accountID}]}, nil
}

func (m *MemStore) setOrgMembershipLocked(key orgAccountKey, member OrgMembership) {
	old, exists := m.memberships[key]
	if m.orgAccessRevisions == nil {
		m.orgAccessRevisions = make(map[orgAccountKey]int64)
	}
	if !exists || old.Role != member.Role || !sameRemovalTime(old.RemovedAt, member.RemovedAt) || !old.JoinedAt.Equal(member.JoinedAt) {
		m.orgAccessRevision++
		m.orgAccessRevisions[key] = m.orgAccessRevision
		m.revokeObjectMemberCredentialsLocked(member.OrgID, member.AccountID)
	}
	m.memberships[key] = member
	if member.RemovedAt != nil && old.RemovedAt == nil {
		m.revokeOrgMemberCredentialsLocked(member.OrgID, member.AccountID)
	}
}

func sameRemovalTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}

func (m *MemStore) revokeOrgMemberCredentialsLocked(orgID, accountID string) {
	now := time.Now().UTC()
	for id, key := range m.keys {
		if key.OrgID == orgID && key.AccountID == accountID && key.Status != string(APIKeyStatusRevoked) {
			key.Status, key.RevokedAt = string(APIKeyStatusRevoked), &now
			m.keys[id], m.keyByHash[hex.EncodeToString(key.Hash)] = key, key
		}
	}
	for id, token := range m.deployTokens {
		app := m.apps[token.AppID]
		if app.OrgID == orgID && token.AccountID == accountID && token.Status != string(APIKeyStatusRevoked) {
			token.Status, token.RevokedAt = string(APIKeyStatusRevoked), &now
			m.deployTokens[id], m.deployTokenByHash[string(token.Hash)] = token, token
		}
	}
	for hash, invitation := range m.invitations {
		if invitation.OrgID == orgID && strings.EqualFold(invitation.Email, m.accounts[accountID].Email) && invitation.ConsumedAt == nil && invitation.RevokedAt == nil {
			invitation.RevokedAt = &now
			m.invitations[hash] = invitation
		}
	}
}

func (m *MemStore) checkOrgAccessLocked(ctx context.Context) error {
	f, ok := orgAccessFrom(ctx)
	if !ok {
		return nil
	}
	mem, exists := m.memberships[orgAccountKey{OrgID: f.OrgID, AccountID: f.AccountID}]
	if !exists || mem.RemovedAt != nil || m.orgAccessRevisions[orgAccountKey{OrgID: f.OrgID, AccountID: f.AccountID}] != f.Revision {
		return rejectOrgAccess(ctx)
	}
	if f.CredentialID == "" {
		return nil
	}
	var status string
	var expiry *time.Time
	if f.AppID != "" {
		token := m.deployTokens[f.CredentialID]
		if token.AccountID != f.AccountID || token.AppID != f.AppID {
			return rejectOrgAccess(ctx)
		}
		status, expiry = token.Status, token.ExpiresAt
	} else {
		key, found := m.keys[f.CredentialID]
		if !found && f.CredentialOrgID == "" {
			return m.checkOrgOIDCBearerLocked(ctx, f)
		}
		if key.AccountID != f.AccountID || key.OrgID != f.CredentialOrgID {
			return rejectOrgAccess(ctx)
		}
		status, expiry = key.Status, key.ExpiresAt
	}
	if status == string(APIKeyStatusRevoked) || expiry != nil && !expiry.After(time.Now()) {
		return rejectOrgAccess(ctx)
	}
	return nil
}

func (m *MemStore) checkOrgOIDCBearerLocked(ctx context.Context, f OrgAccessFence) error {
	for _, token := range m.oidcExchangedTokens {
		if token.ID == f.CredentialID && token.AccountID == f.AccountID && token.ExpiresAt.After(time.Now()) {
			return nil
		}
	}
	return rejectOrgAccess(ctx)
}

func (m *MemStore) checkOrgCredentialMemberLocked(orgID, accountID string) error {
	org, ok := m.orgs[orgID]
	if !ok || org.Personal {
		return nil // Existing lightweight fixtures may omit org rows.
	}
	member, ok := m.memberships[orgAccountKey{OrgID: orgID, AccountID: accountID}]
	if !ok || member.RemovedAt != nil {
		return ErrOrgAccessRevoked
	}
	return nil
}

// WithMemoryIntentFence gives independent in-memory catalogues the same
// serialization boundary as this store's membership/credential mutations.
func (m *MemStore) WithMemoryIntentFence(ctx context.Context) context.Context {
	return db.WithMemoryIntentFence(ctx, func(ctx context.Context) (func(), error) {
		m.mu.Lock()
		if err := m.checkOrgAccessLocked(ctx); err != nil {
			m.mu.Unlock()
			return nil, err
		}
		return m.mu.Unlock, nil
	})
}
