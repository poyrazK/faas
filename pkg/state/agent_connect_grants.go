package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AgentConnectGrant is the Connect consent that approves a hosted MCP key's
// deploys, promotions, checked rollbacks, restarts and undo
// (ADR-20261127160000): who approved the sign-in, and when.
type AgentConnectGrant struct {
	KeyID     string
	GrantedBy string
	GrantedAt time.Time
}

// AgentConnectGrantStore keeps Connect grants. apid writes one when it mints
// an mcp-login key from a sign-in the person approved with deploy access.
type AgentConnectGrantStore interface {
	// CreateAPIKeyConnectGrant records the grant once; it fails when the key
	// is gone or already has one.
	CreateAPIKeyConnectGrant(ctx context.Context, keyID, grantedBy string) error
	// APIKeyConnectGrant returns the key's grant; false when it has none.
	APIKeyConnectGrant(ctx context.Context, keyID string) (AgentConnectGrant, bool, error)
}

var (
	_ AgentConnectGrantStore = (*PgStore)(nil)
	_ AgentConnectGrantStore = (*MemStore)(nil)
)

func (s *PgStore) CreateAPIKeyConnectGrant(ctx context.Context, keyID, grantedBy string) error {
	return mapErr(sqlc.New().InsertAPIKeyConnectGrant(ctx, s.pool, sqlc.InsertAPIKeyConnectGrantParams{
		KeyID: mustPgUUID(keyID), GrantedBy: mustPgUUID(grantedBy),
	}))
}

func (s *PgStore) APIKeyConnectGrant(ctx context.Context, keyID string) (AgentConnectGrant, bool, error) {
	row, err := sqlc.New().GetAPIKeyConnectGrant(ctx, s.pool, mustPgUUID(keyID))
	if err != nil {
		if err = mapErr(err); errors.Is(err, ErrNotFound) {
			return AgentConnectGrant{}, false, nil
		}
		return AgentConnectGrant{}, false, err
	}
	return AgentConnectGrant{KeyID: keyID, GrantedBy: pgUUIDString(row.GrantedBy), GrantedAt: row.GrantedAt.Time}, true, nil
}

func (m *MemStore) CreateAPIKeyConnectGrant(_ context.Context, keyID, grantedBy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasKeyLocked(keyID) {
		return ErrNotFound
	}
	if _, ok := m.keyConnectGrants[keyID]; ok {
		return ErrConflict
	}
	if m.keyConnectGrants == nil {
		m.keyConnectGrants = map[string]AgentConnectGrant{}
	}
	m.keyConnectGrants[keyID] = AgentConnectGrant{KeyID: keyID, GrantedBy: grantedBy, GrantedAt: time.Now().UTC()}
	return nil
}

func (m *MemStore) APIKeyConnectGrant(_ context.Context, keyID string) (AgentConnectGrant, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	grant, ok := m.keyConnectGrants[keyID]
	if !ok || !m.hasKeyLocked(keyID) {
		return AgentConnectGrant{}, false, nil
	}
	return grant, true, nil
}
