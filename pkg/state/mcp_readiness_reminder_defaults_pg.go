package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func readReadinessReminderDefaultsPG(ctx context.Context, db sqlc.DBTX, id string) (api.MCPReadinessReminderDefaults, error) {
	row, err := sqlc.New().ReadMCPReminderDefaults(ctx, db, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return api.MCPReadinessReminderDefaults{}, nil
	}
	if err != nil {
		return api.MCPReadinessReminderDefaults{}, err
	}
	return api.MCPReadinessReminderDefaults{Version: row.Version, ReminderPolicy: api.MCPReadinessReminderPolicy{IntervalSeconds: int(row.IntervalSeconds), MaxReminders: int(row.MaxReminders)}}, nil
}
func initializeReadinessReminderDefaultsPG(ctx context.Context, tx pgx.Tx, rule, incident string) error {
	defaults, err := readReadinessReminderDefaultsPG(ctx, tx, rule)
	if err != nil {
		return err
	}
	return saveReadinessAttentionPG(ctx, tx, incident, readinessReminderDefaultsAttention(defaults))
}
func (s *PgStore) GetMCPReadinessReminderDefaults(ctx context.Context, account, app, id string) (api.MCPReadinessReminderDefaults, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.MCPReadinessReminderDefaults{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.ReadMCPNotificationOwnedApp(ctx, tx, sqlc.ReadMCPNotificationOwnedAppParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account)}); err != nil {
		return api.MCPReadinessReminderDefaults{}, mapErr(err)
	}
	row, err := q.ReadCustomerAlertRule(ctx, tx, mustPgUUID(id))
	if err != nil {
		return api.MCPReadinessReminderDefaults{}, mapErr(err)
	}
	if err = readinessWebhookRule(customerAlertRule(row), account, app); err != nil {
		return api.MCPReadinessReminderDefaults{}, err
	}
	out, err := readReadinessReminderDefaultsPG(ctx, tx, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) SetMCPReadinessReminderDefaults(ctx context.Context, account, app, id string, expected int64, policy api.MCPReadinessReminderPolicy) (api.MCPReadinessReminderDefaults, error) {
	if api.ValidateMCPReadinessReminderDefaultsRequest(api.SetMCPReadinessReminderDefaultsRequest{ExpectedVersion: &expected, ReminderPolicy: &policy}) != nil {
		return api.MCPReadinessReminderDefaults{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.MCPReadinessReminderDefaults{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockReadinessApp(ctx, tx, account, app); err != nil {
		return api.MCPReadinessReminderDefaults{}, err
	}
	rule, err := lockReadinessRule(ctx, tx, id)
	if err != nil {
		return api.MCPReadinessReminderDefaults{}, err
	}
	if err = readinessWebhookRule(rule, account, app); err != nil {
		return api.MCPReadinessReminderDefaults{}, err
	}
	old, err := readReadinessReminderDefaultsPG(ctx, tx, id)
	if err != nil {
		return api.MCPReadinessReminderDefaults{}, err
	}
	if old.Version != expected {
		return api.MCPReadinessReminderDefaults{}, ErrConflict
	}
	out := api.MCPReadinessReminderDefaults{Version: expected + 1, ReminderPolicy: policy}
	if err = sqlc.New().SaveMCPReminderDefaults(ctx, tx, sqlc.SaveMCPReminderDefaultsParams{RuleID: mustPgUUID(id), Version: out.Version, IntervalSeconds: int32(policy.IntervalSeconds), MaxReminders: int32(policy.MaxReminders)}); err != nil {
		return api.MCPReadinessReminderDefaults{}, err
	}
	return out, tx.Commit(ctx)
}
