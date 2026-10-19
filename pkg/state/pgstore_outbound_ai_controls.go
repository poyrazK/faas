package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIControlStore = (*PgStore)(nil)

// SetOutboundBindingAIGuardrails replaces one app's AI guardrails on an AI
// integration binding (ADR-991).
func (s *PgStore) SetOutboundBindingAIGuardrails(ctx context.Context, accountID, appID, integrationID string, guard OutboundAIGuardrails) error {
	if !guard.valid() {
		return ErrInvalidArgument
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE outbound_app_bindings binding
		   SET ai_requests_per_minute = $4, ai_tokens_per_minute = $5, ai_allowed_models = $6, ai_max_output_tokens = $7
		  FROM outbound_integrations integration
		 WHERE binding.account_id = $1 AND binding.app_id = $2 AND binding.integration_id = $3
		   AND integration.id = binding.integration_id AND integration.account_id = $1
		   AND integration.ai_provider IS NOT NULL`,
		mustPgUUID(accountID), mustPgUUID(appID), mustPgUUID(integrationID),
		guard.RequestsPerMinute, guard.TokensPerMinute, guard.AllowedModels, guard.MaxOutputTokens)
	if err != nil {
		return mapErr(err)
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// outboundManagedIntegrationExists checks that an account owns a managed
// integration, so reads of its children return 404 for anyone else.
func (s *PgStore) outboundManagedIntegrationExists(ctx context.Context, accountID, integrationID string) error {
	var found int
	err := s.pool.QueryRow(ctx, `
		SELECT 1 FROM outbound_integrations
		 WHERE account_id = $1 AND id = $2 AND provider_auth_mode = 'managed'`,
		mustPgUUID(accountID), mustPgUUID(integrationID)).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return mapErr(err)
}

// ListOutboundCredentialKeys lists an integration's extra keys without
// their sealed values.
func (s *PgStore) ListOutboundCredentialKeys(ctx context.Context, accountID, integrationID string) ([]OutboundCredentialKey, error) {
	if err := s.outboundManagedIntegrationExists(ctx, accountID, integrationID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, label, created_at FROM outbound_integration_credential_keys
		 WHERE account_id = $1 AND integration_id = $2
		 ORDER BY created_at, id`, mustPgUUID(accountID), mustPgUUID(integrationID))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	keys := make([]OutboundCredentialKey, 0)
	for rows.Next() {
		var id pgtype.UUID
		key := OutboundCredentialKey{AccountID: accountID, IntegrationID: integrationID}
		if err := rows.Scan(&id, &key.Label, &key.CreatedAt); err != nil {
			return nil, mapErr(err)
		}
		key.ID = pgUUIDString(id)
		keys = append(keys, key)
	}
	return keys, mapErr(rows.Err())
}

// CreateOutboundCredentialKey adds a sealed key to a customer-sealed managed
// integration, serialized on the integration row so the pool ceiling holds.
func (s *PgStore) CreateOutboundCredentialKey(ctx context.Context, key OutboundCredentialKey) (OutboundCredentialKey, error) {
	if !api.ValidOutboundCredentialKeyLabel(key.Label) || len(key.AuthorizationSealed) == 0 || len(key.AuthorizationSealed) > 65536 {
		return OutboundCredentialKey{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OutboundCredentialKey{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM outbound_integrations
		 WHERE account_id = $1 AND id = $2 AND provider_auth_mode = 'managed'
		   AND credential_source = 'customer_sealed' AND enabled
		 FOR UPDATE`, mustPgUUID(key.AccountID), mustPgUUID(key.IntegrationID)).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboundCredentialKey{}, ErrNotFound
	}
	if err != nil {
		return OutboundCredentialKey{}, mapErr(err)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM outbound_integration_credential_keys WHERE integration_id = $1`,
		mustPgUUID(key.IntegrationID)).Scan(&count); err != nil {
		return OutboundCredentialKey{}, mapErr(err)
	}
	if count >= api.OutboundIntegrationCredentialKeysMax {
		return OutboundCredentialKey{}, ErrOutboundCredentialKeyLimit
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO outbound_integration_credential_keys (id, integration_id, account_id, label, authorization_sealed)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at`, mustPgUUID(key.ID), mustPgUUID(key.IntegrationID), mustPgUUID(key.AccountID),
		key.Label, key.AuthorizationSealed).Scan(&key.CreatedAt); err != nil {
		return OutboundCredentialKey{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OutboundCredentialKey{}, mapErr(err)
	}
	key.AuthorizationSealed = nil
	return key, nil
}

// DeleteOutboundCredentialKey removes one extra key; outboundd stops using
// it on its next call because it reads keys per call.
func (s *PgStore) DeleteOutboundCredentialKey(ctx context.Context, accountID, integrationID, keyID string) error {
	command, err := s.pool.Exec(ctx, `
		DELETE FROM outbound_integration_credential_keys
		 WHERE account_id = $1 AND integration_id = $2 AND id = $3`,
		mustPgUUID(accountID), mustPgUUID(integrationID), mustPgUUID(keyID))
	if err != nil {
		return mapErr(err)
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// ListOutboundAIBudgetAlerts lists an integration's alerts, newest first.
func (s *PgStore) ListOutboundAIBudgetAlerts(ctx context.Context, accountID, integrationID string, limit int) ([]OutboundAIBudgetAlert, error) {
	if err := s.outboundManagedIntegrationExists(ctx, accountID, integrationID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, budget, app_id, COALESCE(end_user, ''), period_start, threshold_percent, limit_value, used_value, crossed_at,
		       COALESCE(tag, '')
		  FROM outbound_ai_budget_alerts
		 WHERE account_id = $1 AND integration_id = $2
		 ORDER BY crossed_at DESC, threshold_percent DESC, id
		 LIMIT $3`, mustPgUUID(accountID), mustPgUUID(integrationID), boundedPage(limit, api.OutboundAIBudgetAlertsPageMax))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	alerts := make([]OutboundAIBudgetAlert, 0)
	for rows.Next() {
		var id, appID pgtype.UUID
		var periodStart time.Time
		alert := OutboundAIBudgetAlert{IntegrationID: integrationID}
		if err := rows.Scan(&id, &alert.Budget, &appID, &alert.EndUser, &periodStart, &alert.ThresholdPercent, &alert.Limit, &alert.Used,
			&alert.CrossedAt, &alert.Tag); err != nil {
			return nil, mapErr(err)
		}
		alert.ID, alert.PeriodStart, alert.CrossedAt = pgUUIDString(id), periodStart.UTC(), alert.CrossedAt.UTC()
		if appID.Valid {
			alert.AppID = pgUUIDString(appID)
		}
		alerts = append(alerts, alert)
	}
	return alerts, mapErr(rows.Err())
}

// ListOutboundAIRequestLog lists logged calls newest first, before an
// optional created_at cursor.
func (s *PgStore) ListOutboundAIRequestLog(ctx context.Context, accountID, integrationID string, before *time.Time, limit int) ([]OutboundAIRequestLogEntry, error) {
	if err := s.outboundManagedIntegrationExists(ctx, accountID, integrationID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT log.id, log.created_at, log.caller, log.route_id, log.model, log.status, log.outcome,
		       log.input_tokens, log.cached_input_tokens, log.cache_write_tokens, log.output_tokens,
		       log.cost_millicents, log.duration_ms, log.ttfb_ms, log.attempts, COALESCE(log.end_user, ''),
		       EXISTS (SELECT 1 FROM outbound_ai_request_content content WHERE content.log_id = log.id),
		       COALESCE(log.tags, '{}'::text[])
		  FROM outbound_ai_request_log log
		 WHERE log.integration_id = $1 AND ($2::timestamptz IS NULL OR log.created_at < $2)
		 ORDER BY log.created_at DESC, log.id DESC
		 LIMIT $3`, mustPgUUID(integrationID), before, boundedPage(limit, api.OutboundAIRequestLogPageMax))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	entries := make([]OutboundAIRequestLogEntry, 0)
	for rows.Next() {
		var id, routeID pgtype.UUID
		var status, attempts int16
		var duration int32
		var ttfb pgtype.Int4
		var entry OutboundAIRequestLogEntry
		if err := rows.Scan(&id, &entry.CreatedAt, &entry.Caller, &routeID, &entry.Model, &status, &entry.Outcome,
			&entry.InputTokens, &entry.CachedInputTokens, &entry.CacheWriteTokens, &entry.OutputTokens,
			&entry.CostMillicents, &duration, &ttfb, &attempts, &entry.EndUser, &entry.ContentCaptured, &entry.Tags); err != nil {
			return nil, mapErr(err)
		}
		entry.ID, entry.CreatedAt = pgUUIDString(id), entry.CreatedAt.UTC()
		entry.Status, entry.Attempts, entry.DurationMS = int(status), int(attempts), int64(duration)
		if routeID.Valid {
			entry.RouteID = pgUUIDString(routeID)
		}
		if ttfb.Valid {
			value := int64(ttfb.Int32)
			entry.TTFBMS = &value
		}
		entries = append(entries, entry)
	}
	return entries, mapErr(rows.Err())
}

var _ OutboundAIContentStore = (*PgStore)(nil)

// GetOutboundAIRequestContent reads one logged call's captured bodies
// (ADR-996). A call without a capture, or one outside the account's
// integration, is not found.
func (s *PgStore) GetOutboundAIRequestContent(ctx context.Context, accountID, integrationID, logID string) (OutboundAIRequestContent, error) {
	if _, err := uuid.Parse(logID); err != nil {
		return OutboundAIRequestContent{}, ErrNotFound
	}
	if err := s.outboundManagedIntegrationExists(ctx, accountID, integrationID); err != nil {
		return OutboundAIRequestContent{}, err
	}
	content := OutboundAIRequestContent{LogID: logID}
	err := s.pool.QueryRow(ctx, `
		SELECT created_at, request_body, request_truncated, response_body, response_truncated,
		       COALESCE(response_content_type, ''), COALESCE(request_path, '')
		  FROM outbound_ai_request_content
		 WHERE integration_id = $1 AND log_id = $2`, mustPgUUID(integrationID), mustPgUUID(logID)).
		Scan(&content.CreatedAt, &content.RequestBody, &content.RequestTruncated, &content.ResponseBody,
			&content.ResponseTruncated, &content.ResponseContentType, &content.RequestPath)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboundAIRequestContent{}, ErrNotFound
	}
	if err != nil {
		return OutboundAIRequestContent{}, mapErr(err)
	}
	content.CreatedAt = content.CreatedAt.UTC()
	return content, nil
}
