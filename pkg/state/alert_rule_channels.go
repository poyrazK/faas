package state

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AlertRuleChannelStore binds alert rules to notification channels
// (ADR-749). Optional on Store; callers type-assert.
type AlertRuleChannelStore interface {
	// SetAlertRuleChannels replaces a rule's channels. Channels not owned
	// by the rule's account are ignored, so callers validate first.
	SetAlertRuleChannels(ctx context.Context, ruleID string, channelIDs []string) error
	// ListAlertRuleChannels returns the channels a rule delivers to, with
	// their sealed destinations.
	ListAlertRuleChannels(ctx context.Context, ruleID string) ([]NotificationChannel, error)
}

// SetAlertRuleChannels replaces the bindings in one transaction.
func (s *PgStore) SetAlertRuleChannels(ctx context.Context, ruleID string, channelIDs []string) error {
	rule, err := alertPgUUID(ruleID)
	if err != nil || !rule.Valid {
		return ErrInvalidArgument
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return setAlertRuleChannelsTx(ctx, tx, rule, channelIDs)
	})
}

func setAlertRuleChannelsTx(ctx context.Context, tx pgx.Tx, rule pgtype.UUID, channelIDs []string) error {
	ids := make([]pgtype.UUID, 0, len(channelIDs))
	for _, id := range channelIDs {
		key, err := alertPgUUID(id)
		if err != nil || !key.Valid {
			return ErrInvalidArgument
		}
		ids = append(ids, key)
	}
	q := sqlc.New()
	if err := q.DeleteAlertRuleChannels(ctx, tx, rule); err != nil {
		return fmt.Errorf("state: clear alert rule channels: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	if err := q.InsertAlertRuleChannels(ctx, tx, sqlc.InsertAlertRuleChannelsParams{ChannelIds: ids, RuleID: rule}); err != nil {
		return fmt.Errorf("state: bind alert rule channels: %w", err)
	}
	return nil
}

// ListAlertRuleChannels returns a rule's channels in name order.
func (s *PgStore) ListAlertRuleChannels(ctx context.Context, ruleID string) ([]NotificationChannel, error) {
	rule, err := alertPgUUID(ruleID)
	if err != nil || !rule.Valid {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListAlertRuleChannels(ctx, s.pool, rule)
	if err != nil {
		return nil, fmt.Errorf("state: list alert rule channels: %w", err)
	}
	out := make([]NotificationChannel, 0, len(rows))
	for _, row := range rows {
		out = append(out, channelFromRow(row))
	}
	return out, nil
}

// SetAlertRuleChannels mirrors PgStore, keeping only the rule account's
// channels.
func (m *MemStore) SetAlertRuleChannels(_ context.Context, ruleID string, channelIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rule, ok := m.alertRules[ruleID]
	if !ok {
		return nil
	}
	m.storeAlertRuleChannelsLocked(ruleID, m.ownedAlertRuleChannelsLocked(rule.AccountID, channelIDs))
	return nil
}

func (m *MemStore) ownedAlertRuleChannelsLocked(accountID string, channelIDs []string) []string {
	owned := map[string]bool{}
	for _, c := range m.notificationChannels[accountID] {
		owned[c.ID] = true
	}
	var kept []string
	for _, id := range channelIDs {
		if owned[id] {
			kept = append(kept, id)
			owned[id] = false // dedupe
		}
	}
	return kept
}

func (m *MemStore) storeAlertRuleChannelsLocked(ruleID string, kept []string) {
	if m.alertRuleChannels == nil {
		m.alertRuleChannels = map[string][]string{}
	}
	m.alertRuleChannels[ruleID] = kept
}

// ListAlertRuleChannels mirrors PgStore; a deleted channel drops out.
func (m *MemStore) ListAlertRuleChannels(_ context.Context, ruleID string) ([]NotificationChannel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rule, ok := m.alertRules[ruleID]
	if !ok {
		return nil, nil
	}
	return m.listAlertRuleChannelsLocked(rule), nil
}

func (m *MemStore) listAlertRuleChannelsLocked(rule AlertRule) []NotificationChannel {
	bound := map[string]bool{}
	for _, id := range m.alertRuleChannels[rule.ID] {
		bound[id] = true
	}
	var out []NotificationChannel
	for _, c := range m.notificationChannels[rule.AccountID] {
		if bound[c.ID] {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

var (
	_ AlertRuleChannelStore = (*PgStore)(nil)
	_ AlertRuleChannelStore = (*MemStore)(nil)
)
