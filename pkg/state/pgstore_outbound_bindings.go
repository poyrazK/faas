package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var _ OutboundBindingStore = (*PgStore)(nil)

func scanOutboundOffer(row pgx.Row) (OutboundIntegrationOffer, error) {
	var id, accountID pgtype.UUID
	var offer OutboundIntegrationOffer
	if err := row.Scan(&id, &accountID, &offer.Name, &offer.Origin, &offer.AllowedMethods, &offer.AllowedPathPrefixes, &offer.Enabled); err != nil {
		return OutboundIntegrationOffer{}, mapErr(err)
	}
	offer.ID, offer.AccountID = pgUUIDString(id), pgUUIDString(accountID)
	return offer, nil
}

func scanOutboundBinding(row pgx.Row) (OutboundAppBinding, error) {
	var id, accountID, appID pgtype.UUID
	var binding OutboundAppBinding
	if err := row.Scan(&id, &accountID, &appID, &binding.Name, &binding.Origin,
		&binding.AllowedMethods, &binding.AllowedPathPrefixes, &binding.Enabled, &binding.CreatedAt); err != nil {
		return OutboundAppBinding{}, mapErr(err)
	}
	binding.ID, binding.AccountID, binding.AppID = pgUUIDString(id), pgUUIDString(accountID), pgUUIDString(appID)
	return binding, nil
}

func (s *PgStore) ListOutboundIntegrationOffers(ctx context.Context, accountID string) ([]OutboundIntegrationOffer, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, account_id, name, origin, allowed_methods, allowed_path_prefixes, enabled
		  FROM outbound_integrations
		 WHERE account_id = $1 AND provider_auth_mode = 'managed' AND enabled
		   AND cardinality(allowed_methods) > 0 AND cardinality(allowed_path_prefixes) > 0
		 ORDER BY name, id`, mustPgUUID(accountID))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]OutboundIntegrationOffer, 0)
	for rows.Next() {
		offer, err := scanOutboundOffer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, offer)
	}
	return out, mapErr(rows.Err())
}

func (s *PgStore) ListOutboundAppBindings(ctx context.Context, accountID, appID string) ([]OutboundAppBinding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT integration.id, integration.account_id, binding.app_id, integration.name,
		       integration.origin, integration.allowed_methods, integration.allowed_path_prefixes,
		       (integration.enabled AND integration.provider_auth_mode = 'managed'
		        AND cardinality(integration.allowed_methods) > 0
		        AND cardinality(integration.allowed_path_prefixes) > 0), binding.created_at
		  FROM outbound_app_bindings binding
		  JOIN outbound_integrations integration ON integration.id = binding.integration_id
		  JOIN apps app ON app.id = binding.app_id
		 WHERE binding.account_id = $1 AND binding.app_id = $2
		   AND integration.account_id = $1 AND app.account_id = $1 AND app.status <> 'deleted'
		 ORDER BY integration.name, integration.id`, mustPgUUID(accountID), mustPgUUID(appID))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]OutboundAppBinding, 0)
	for rows.Next() {
		binding, err := scanOutboundBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, binding)
	}
	return out, mapErr(rows.Err())
}

func (s *PgStore) BindOutboundIntegration(ctx context.Context, accountID, appID, integrationID string) (OutboundAppBinding, error) {
	account, app, integration := mustPgUUID(accountID), mustPgUUID(appID), mustPgUUID(integrationID)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO outbound_app_bindings (account_id, app_id, integration_id)
		SELECT $1, app.id, integration.id
		  FROM apps app
		  JOIN outbound_integrations integration ON integration.id = $3
		 WHERE app.id = $2 AND app.account_id = $1 AND app.status <> 'deleted'
		   AND integration.account_id = $1 AND integration.provider_auth_mode = 'managed'
		   AND integration.enabled
		   AND cardinality(integration.allowed_methods) > 0
		   AND cardinality(integration.allowed_path_prefixes) > 0
		ON CONFLICT (app_id, integration_id) DO NOTHING`, account, app, integration)
	if err != nil {
		return OutboundAppBinding{}, mapErr(err)
	}
	return scanOutboundBinding(s.pool.QueryRow(ctx, `
		SELECT integration.id, integration.account_id, binding.app_id, integration.name,
		       integration.origin, integration.allowed_methods, integration.allowed_path_prefixes,
		       integration.enabled, binding.created_at
		  FROM outbound_app_bindings binding
		  JOIN outbound_integrations integration ON integration.id = binding.integration_id
		 WHERE binding.account_id = $1 AND binding.app_id = $2 AND binding.integration_id = $3
		   AND integration.account_id = $1
		   AND integration.provider_auth_mode = 'managed' AND integration.enabled
		   AND cardinality(integration.allowed_methods) > 0
		   AND cardinality(integration.allowed_path_prefixes) > 0`, account, app, integration))
}

func (s *PgStore) UnbindOutboundIntegration(ctx context.Context, accountID, appID, integrationID string) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM outbound_app_bindings
		 WHERE account_id = $1 AND app_id = $2 AND integration_id = $3`,
		mustPgUUID(accountID), mustPgUUID(appID), mustPgUUID(integrationID))
	return mapErr(err)
}
