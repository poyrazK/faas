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
	if err := row.Scan(&id, &accountID, &offer.Name, &offer.Origin, &offer.AllowedMethods, &offer.AllowedPathPrefixes,
		&offer.Enabled, &offer.CredentialSource, &offer.CredentialConfigured); err != nil {
		return OutboundIntegrationOffer{}, mapErr(err)
	}
	offer.ID, offer.AccountID = pgUUIDString(id), pgUUIDString(accountID)
	return offer, nil
}

func scanOutboundBinding(row pgx.Row) (OutboundAppBinding, error) {
	var id, accountID, appID pgtype.UUID
	var binding OutboundAppBinding
	if err := row.Scan(&id, &accountID, &appID, &binding.Name, &binding.Origin,
		&binding.AllowedMethods, &binding.AllowedPathPrefixes, &binding.Enabled,
		&binding.CredentialSource, &binding.CredentialConfigured, &binding.CreatedAt); err != nil {
		return OutboundAppBinding{}, mapErr(err)
	}
	binding.ID, binding.AccountID, binding.AppID = pgUUIDString(id), pgUUIDString(accountID), pgUUIDString(appID)
	return binding, nil
}

func (s *PgStore) ListOutboundIntegrationOffers(ctx context.Context, accountID string) ([]OutboundIntegrationOffer, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT integration.id, integration.account_id, integration.name, integration.origin,
		       integration.allowed_methods, integration.allowed_path_prefixes, integration.enabled,
		       integration.credential_source,
		       (integration.credential_source = 'operator_env' OR credential.integration_id IS NOT NULL)
		  FROM outbound_integrations integration
		  LEFT JOIN outbound_integration_credentials credential
		    ON credential.integration_id = integration.id AND credential.account_id = integration.account_id
		 WHERE integration.account_id = $1 AND integration.provider_auth_mode = 'managed' AND integration.enabled
		   AND cardinality(integration.allowed_methods) > 0 AND cardinality(integration.allowed_path_prefixes) > 0
		 ORDER BY integration.name, integration.id`, mustPgUUID(accountID))
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
		        AND cardinality(integration.allowed_path_prefixes) > 0), integration.credential_source,
		       (integration.credential_source = 'operator_env' OR credential.integration_id IS NOT NULL),
		       binding.created_at
		  FROM outbound_app_bindings binding
		  JOIN outbound_integrations integration ON integration.id = binding.integration_id
		  JOIN apps app ON app.id = binding.app_id
		  LEFT JOIN outbound_integration_credentials credential
		    ON credential.integration_id = integration.id AND credential.account_id = integration.account_id
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
		       integration.enabled, integration.credential_source,
		       (integration.credential_source = 'operator_env' OR credential.integration_id IS NOT NULL),
		       binding.created_at
		  FROM outbound_app_bindings binding
		  JOIN outbound_integrations integration ON integration.id = binding.integration_id
		  LEFT JOIN outbound_integration_credentials credential
		    ON credential.integration_id = integration.id AND credential.account_id = integration.account_id
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

func (s *PgStore) SetOutboundCredential(ctx context.Context, accountID, integrationID string, sealed []byte) error {
	if len(sealed) == 0 {
		return ErrNotFound
	}
	command, err := s.pool.Exec(ctx, `
		WITH eligible AS (
			SELECT integration.id, integration.account_id
			  FROM outbound_integrations integration
			 WHERE integration.id = $2 AND integration.account_id = $1
			   AND integration.enabled AND integration.provider_auth_mode = 'managed'
			   AND integration.credential_source = 'customer_sealed'
			 FOR UPDATE OF integration
		)
		INSERT INTO outbound_integration_credentials (integration_id, account_id, authorization_sealed)
		SELECT eligible.id, eligible.account_id, $3 FROM eligible
		ON CONFLICT (integration_id) DO UPDATE SET
		    authorization_sealed = EXCLUDED.authorization_sealed, updated_at = now()
		WHERE outbound_integration_credentials.account_id = EXCLUDED.account_id`,
		mustPgUUID(accountID), mustPgUUID(integrationID), sealed)
	if err != nil {
		return mapErr(err)
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) DeleteOutboundCredential(ctx context.Context, accountID, integrationID string) error {
	account, integration := mustPgUUID(accountID), mustPgUUID(integrationID)
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM outbound_integrations
			 WHERE id = $2 AND account_id = $1 AND provider_auth_mode = 'managed'
		   AND credential_source = 'customer_sealed')`, account, integration).Scan(&exists)
	if err != nil {
		return mapErr(err)
	}
	if !exists {
		return ErrNotFound
	}
	_, err = s.pool.Exec(ctx, `
		DELETE FROM outbound_integration_credentials
		 WHERE account_id = $1 AND integration_id = $2`, account, integration)
	return mapErr(err)
}
