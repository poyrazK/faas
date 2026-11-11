package state

import (
	"context"
	"slices"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AgentApprovalChannelStore keeps the notification channels that get agent
// approval requests (ADR-20261107140000 §3). apid is the only writer.
type AgentApprovalChannelStore interface {
	// SetAgentApprovalChannels replaces one scope's bound channels: orgID ""
	// routes the requests of the account's agent keys, otherwise those of
	// keys acting in the organization. Channels must be the account's own;
	// ErrNotFound names none that is not.
	SetAgentApprovalChannels(ctx context.Context, accountID, orgID string, channelIDs []string) error
	// AgentApprovalChannels lists one scope's bound channels, by name.
	AgentApprovalChannels(ctx context.Context, accountID, orgID string) ([]NotificationChannel, error)
}

func (s *PgStore) SetAgentApprovalChannels(ctx context.Context, accountID, orgID string, channelIDs []string) error {
	q := sqlc.New()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := q.DeleteAgentApprovalChannelBindings(ctx, tx, sqlc.DeleteAgentApprovalChannelBindingsParams{
		AccountID: mustPgUUID(accountID), OrgID: mustPgUUID(orgID),
	}); err != nil {
		return mapErr(err)
	}
	for _, id := range channelIDs {
		if err := q.InsertAgentApprovalChannelBinding(ctx, tx, sqlc.InsertAgentApprovalChannelBindingParams{
			AccountID: mustPgUUID(accountID), OrgID: mustPgUUID(orgID), ChannelID: mustPgUUID(id),
		}); err != nil {
			return mapErr(err)
		}
	}
	return mapErr(tx.Commit(ctx))
}

func (s *PgStore) AgentApprovalChannels(ctx context.Context, accountID, orgID string) ([]NotificationChannel, error) {
	rows, err := sqlc.New().ListAgentApprovalChannelBindings(ctx, s.pool, sqlc.ListAgentApprovalChannelBindingsParams{
		AccountID: mustPgUUID(accountID), OrgID: mustPgUUID(orgID),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return channelsFromRows(rows), nil
}

// NotificationChannelsForAgentApproval returns the channels a request
// reaches: those the connector bound for its own keys, and those bound for
// the organization the key acts in.
func (s *PgStore) NotificationChannelsForAgentApproval(ctx context.Context, accountID, orgID string) ([]NotificationChannel, error) {
	rows, err := sqlc.New().NotificationChannelsForAgentApproval(ctx, s.pool, sqlc.NotificationChannelsForAgentApprovalParams{
		AccountID: mustPgUUID(accountID), OrgID: mustPgUUID(orgID),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return channelsFromRows(rows), nil
}

// MarkAgentApprovalChannelsNotified records that a request went to its
// channels, so a deferred or retried notification does not send it again.
func (s *PgStore) MarkAgentApprovalChannelsNotified(ctx context.Context, approvalID string) error {
	return mapErr(sqlc.New().MarkAgentApprovalChannelsNotified(ctx, s.pool, mustPgUUID(approvalID)))
}

func channelsFromRows(rows []sqlc.NotificationChannel) []NotificationChannel {
	out := make([]NotificationChannel, 0, len(rows))
	for _, row := range rows {
		out = append(out, channelFromRow(row))
	}
	return out
}

// --- MemStore ---------------------------------------------------------------

type memApprovalChannelBinding struct{ account, org, channel string }

func (m *MemStore) channelOfLocked(accountID, id string) (NotificationChannel, bool) {
	for _, ch := range m.notificationChannels[accountID] {
		if ch.ID == id {
			return ch, true
		}
	}
	return NotificationChannel{}, false
}

func (m *MemStore) SetAgentApprovalChannels(_ context.Context, accountID, orgID string, channelIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.agentApprovalChannelBindings[:0]
	for _, b := range m.agentApprovalChannelBindings {
		if b.account != accountID || b.org != orgID {
			kept = append(kept, b)
		}
	}
	m.agentApprovalChannelBindings = kept
	for _, id := range channelIDs {
		if _, ok := m.channelOfLocked(accountID, id); !ok {
			continue // Postgres inserts nothing for another account's channel
		}
		b := memApprovalChannelBinding{accountID, orgID, id}
		if !slices.Contains(m.agentApprovalChannelBindings, b) {
			m.agentApprovalChannelBindings = append(m.agentApprovalChannelBindings, b)
		}
	}
	return nil
}

func (m *MemStore) AgentApprovalChannels(_ context.Context, accountID, orgID string) ([]NotificationChannel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.boundChannelsLocked(func(b memApprovalChannelBinding) bool { return b.account == accountID && b.org == orgID }), nil
}

func (m *MemStore) NotificationChannelsForAgentApproval(_ context.Context, accountID, orgID string) ([]NotificationChannel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.boundChannelsLocked(func(b memApprovalChannelBinding) bool {
		return b.account == accountID && b.org == "" || orgID != "" && b.org == orgID
	}), nil
}

func (m *MemStore) boundChannelsLocked(keep func(memApprovalChannelBinding) bool) []NotificationChannel {
	seen := map[string]bool{}
	out := []NotificationChannel{}
	for _, b := range m.agentApprovalChannelBindings {
		if !keep(b) || seen[b.channel] {
			continue
		}
		if ch, ok := m.channelOfLocked(b.account, b.channel); ok {
			seen[b.channel] = true
			out = append(out, ch)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *MemStore) MarkAgentApprovalChannelsNotified(_ context.Context, approvalID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n, ok := m.agentApprovalNotifications[approvalID]; ok {
		n.ChannelsNotified = true
	}
	return nil
}
