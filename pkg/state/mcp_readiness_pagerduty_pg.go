package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func readReadinessPagerDutyPG(ctx context.Context, db sqlc.DBTX, id string) (MCPReadinessPagerDutyConfig, error) {
	row, err := sqlc.New().ReadMCPPagerDutyPolicy(ctx, db, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPReadinessPagerDutyConfig{}, nil
	}
	if err != nil {
		return MCPReadinessPagerDutyConfig{}, err
	}
	return MCPReadinessPagerDutyConfig{Version: row.Version, Enabled: row.Enabled, Region: row.Region, IntegrationKeySealed: row.IntegrationKeySealed, Fingerprint: row.Fingerprint}, nil
}
func (s *PgStore) GetMCPReadinessPagerDuty(ctx context.Context, account, app, id string) (api.MCPReadinessPagerDutyPolicy, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.MCPReadinessPagerDutyPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.ReadMCPNotificationOwnedApp(ctx, tx, sqlc.ReadMCPNotificationOwnedAppParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account)}); err != nil {
		return api.MCPReadinessPagerDutyPolicy{}, mapErr(err)
	}
	row, err := q.ReadCustomerAlertRule(ctx, tx, mustPgUUID(id))
	if err != nil {
		return api.MCPReadinessPagerDutyPolicy{}, mapErr(err)
	}
	if err = readinessWebhookRule(customerAlertRule(row), account, app); err != nil {
		return api.MCPReadinessPagerDutyPolicy{}, err
	}
	out, err := readReadinessPagerDutyPG(ctx, tx, id)
	if err != nil {
		return readinessPagerDutyView(out), err
	}
	return readinessPagerDutyView(out), tx.Commit(ctx)
}
func (s *PgStore) SetMCPReadinessPagerDuty(ctx context.Context, account, app, id string, expected int64, policy MCPReadinessPagerDutyConfig) (api.MCPReadinessPagerDutyPolicy, error) {
	if validateReadinessPagerDutyConfig(expected, policy) != nil {
		return api.MCPReadinessPagerDutyPolicy{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.MCPReadinessPagerDutyPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockReadinessApp(ctx, tx, account, app); err != nil {
		return api.MCPReadinessPagerDutyPolicy{}, err
	}
	rule, err := lockReadinessRule(ctx, tx, id)
	if err != nil {
		return api.MCPReadinessPagerDutyPolicy{}, err
	}
	if err = readinessWebhookRule(rule, account, app); err != nil {
		return api.MCPReadinessPagerDutyPolicy{}, err
	}
	old, err := readReadinessPagerDutyPG(ctx, tx, id)
	if err != nil {
		return api.MCPReadinessPagerDutyPolicy{}, err
	}
	if old.Version != expected {
		return api.MCPReadinessPagerDutyPolicy{}, ErrConflict
	}
	fallback, err := readReadinessFallbackPG(ctx, tx, id)
	if err != nil {
		return api.MCPReadinessPagerDutyPolicy{}, err
	}
	if sameReadinessPagerDuty(policy, fallback) {
		return api.MCPReadinessPagerDutyPolicy{}, ErrInvalidArgument
	}
	out := policy
	out.Region = readinessPagerDutyRegion(policy)
	out.Version = expected + 1
	if out.IntegrationKeySealed == nil {
		out.IntegrationKeySealed = []byte{}
	}
	if err = sqlc.New().SaveMCPPagerDutyPolicy(ctx, tx, sqlc.SaveMCPPagerDutyPolicyParams{RuleID: mustPgUUID(id), Version: out.Version, Enabled: out.Enabled, Region: out.Region, IntegrationKeySealed: out.IntegrationKeySealed, Fingerprint: out.Fingerprint}); err != nil {
		return api.MCPReadinessPagerDutyPolicy{}, err
	}
	return readinessPagerDutyView(out), tx.Commit(ctx)
}
