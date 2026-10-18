package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func readReadinessFallbackPG(ctx context.Context, db sqlc.DBTX, id string) (MCPReadinessFallbackConfig, error) {
	pagerDuty, err := readReadinessPagerDutyPG(ctx, db, id)
	if err != nil {
		return MCPReadinessFallbackConfig{}, err
	}
	row, err := sqlc.New().ReadMCPFallbackPolicy(ctx, db, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPReadinessFallbackConfig{PrimaryPagerDuty: pagerDuty}, nil
	}
	if err != nil {
		return MCPReadinessFallbackConfig{}, err
	}
	return MCPReadinessFallbackConfig{Provider: row.Provider, Region: row.Region, IntegrationKeySealed: row.IntegrationKeySealed, Fingerprint: row.Fingerprint, PrimaryPagerDuty: pagerDuty, Version: row.Version, Enabled: row.Enabled, WebhookURL: row.WebhookUrl, WebhookSecretSealed: row.WebhookSecretSealed, AfterFailures: int(row.AfterFailures)}, nil
}
func readReadinessRoutingPG(ctx context.Context, tx pgx.Tx, id string) (readinessIncidentRouting, error) {
	raw, err := sqlc.New().ReadMCPIncidentRouting(ctx, tx, mustPgUUID(id))
	var r readinessIncidentRouting
	if err == nil {
		err = json.Unmarshal(raw, &r)
	}
	return r, err
}
func saveReadinessRoutingPG(ctx context.Context, tx pgx.Tx, id string, r readinessIncidentRouting) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return sqlc.New().SaveMCPIncidentRouting(ctx, tx, sqlc.SaveMCPIncidentRoutingParams{ID: mustPgUUID(id), DeliveryRouting: raw})
}
func (s *PgStore) GetMCPReadinessFallback(ctx context.Context, account, app, id string) (api.MCPReadinessFallbackPolicy, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.MCPReadinessFallbackPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = sqlc.New().ReadMCPNotificationOwnedApp(ctx, tx, sqlc.ReadMCPNotificationOwnedAppParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account)}); err != nil {
		return api.MCPReadinessFallbackPolicy{}, mapErr(err)
	}
	row, err := sqlc.New().ReadCustomerAlertRule(ctx, tx, mustPgUUID(id))
	if err != nil {
		return api.MCPReadinessFallbackPolicy{}, mapErr(err)
	}
	if err = readinessWebhookRule(customerAlertRule(row), account, app); err != nil {
		return api.MCPReadinessFallbackPolicy{}, err
	}
	c, err := readReadinessFallbackPG(ctx, tx, id)
	if err != nil {
		return api.MCPReadinessFallbackPolicy{}, err
	}
	return readinessFallbackView(c), tx.Commit(ctx)
}
func (s *PgStore) SetMCPReadinessFallback(ctx context.Context, account, app, id string, expected int64, c MCPReadinessFallbackConfig) (api.MCPReadinessFallbackPolicy, error) {
	if err := validateReadinessFallbackConfig(c, expected); err != nil {
		return api.MCPReadinessFallbackPolicy{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.MCPReadinessFallbackPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockReadinessApp(ctx, tx, account, app); err != nil {
		return api.MCPReadinessFallbackPolicy{}, err
	}
	rule, err := lockReadinessRule(ctx, tx, id)
	if err != nil {
		return api.MCPReadinessFallbackPolicy{}, err
	}
	if err = readinessWebhookRule(rule, account, app); err != nil {
		return api.MCPReadinessFallbackPolicy{}, err
	}
	old, err := readReadinessFallbackPG(ctx, tx, id)
	if err != nil {
		return api.MCPReadinessFallbackPolicy{}, err
	}
	if old.Version != expected {
		return api.MCPReadinessFallbackPolicy{}, ErrConflict
	}
	if c.Enabled && c.WebhookURL == rule.WebhookURL {
		return api.MCPReadinessFallbackPolicy{}, ErrInvalidArgument
	}
	if sameReadinessPagerDuty(old.PrimaryPagerDuty, c) {
		return api.MCPReadinessFallbackPolicy{}, ErrInvalidArgument
	}
	if c.Provider == "pagerduty" {
		c.Region = readinessPagerDutyRegion(fallbackPagerDuty(c))
	}
	c.Version = expected + 1
	secret := c.WebhookSecretSealed
	if secret == nil {
		secret = []byte{}
	}
	err = sqlc.New().SaveMCPFallbackPolicy(ctx, tx, sqlc.SaveMCPFallbackPolicyParams{RuleID: mustPgUUID(id), Version: c.Version, Enabled: c.Enabled, WebhookUrl: c.WebhookURL, WebhookSecretSealed: secret, AfterFailures: int32(c.AfterFailures), Provider: readinessFallbackProvider(c), Region: c.Region, IntegrationKeySealed: append([]byte{}, c.IntegrationKeySealed...), Fingerprint: c.Fingerprint})
	if err != nil {
		return api.MCPReadinessFallbackPolicy{}, err
	}
	return readinessFallbackView(c), tx.Commit(ctx)
}

func retainReadinessUncertainPrimaryPG(ctx context.Context, tx pgx.Tx, e readinessIncidentEventRecord, now time.Time) error {
	if !readinessPrimaryUncertain(e, now) {
		return nil
	}
	route, err := readReadinessRoutingPG(ctx, tx, e.IncidentID)
	if err != nil {
		return err
	}
	route.PrimaryInformed = true
	return saveReadinessRoutingPG(ctx, tx, e.IncidentID, route)
}
