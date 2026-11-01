package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AgentClient names the client a hosted MCP sign-in's key belongs to
// (ADR-20261030133644): the name its consent page showed and the site it
// was verified at, if any.
type AgentClient struct {
	Name string
	Site string
}

// AgentClientStore records the agent client of a key. apid is the only
// writer.
type AgentClientStore interface {
	// SetAPIKeyAgentClient names the client of one of the account's keys.
	// The first write wins; another account's key is left alone.
	SetAPIKeyAgentClient(ctx context.Context, accountID, keyID string, client AgentClient) error
	// APIKeyAgentClients returns the named clients of the account's usable
	// keys, by key ID.
	APIKeyAgentClients(ctx context.Context, accountID string) (map[string]AgentClient, error)
}

func (s *PgStore) SetAPIKeyAgentClient(ctx context.Context, accountID, keyID string, client AgentClient) error {
	return mapErr(sqlc.New().SetAPIKeyAgentClient(ctx, s.pool, sqlc.SetAPIKeyAgentClientParams{
		KeyID: mustPgUUID(keyID), AccountID: mustPgUUID(accountID), ClientName: client.Name, ClientSite: client.Site,
	}))
}

func (s *PgStore) APIKeyAgentClients(ctx context.Context, accountID string) (map[string]AgentClient, error) {
	rows, err := sqlc.New().ListAPIKeyAgentClients(ctx, s.pool, mustPgUUID(accountID))
	if err != nil {
		return nil, mapErr(err)
	}
	out := make(map[string]AgentClient, len(rows))
	for _, row := range rows {
		out[pgUUIDString(row.KeyID)] = AgentClient{Name: row.ClientName, Site: row.ClientSite}
	}
	return out, nil
}

func (m *MemStore) SetAPIKeyAgentClient(_ context.Context, accountID, keyID string, client AgentClient) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[keyID]
	if !ok || k.AccountID != accountID {
		return nil
	}
	if m.agentClients == nil {
		m.agentClients = map[string]AgentClient{}
	}
	if _, named := m.agentClients[keyID]; !named {
		m.agentClients[keyID] = client
	}
	return nil
}

func (m *MemStore) APIKeyAgentClients(_ context.Context, accountID string) (map[string]AgentClient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]AgentClient{}
	now := time.Now()
	for id, client := range m.agentClients {
		k, ok := m.keys[id]
		if !ok || k.AccountID != accountID || (k.Status != "active" && k.Status != "grace") || (k.ExpiresAt != nil && !k.ExpiresAt.After(now)) {
			continue
		}
		out[id] = client
	}
	return out, nil
}
