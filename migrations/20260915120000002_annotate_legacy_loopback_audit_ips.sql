-- +goose Up
-- +goose StatementBegin
-- Before gatewayd-public preserved Caddy's normalized forwarding context,
-- public auth and key events stored the loopback proxy address. The original
-- client address cannot be reconstructed. Preserve the immutable source rows
-- and append one account-scoped provenance marker instead of rewriting history
-- to an invented value.
WITH affected_accounts AS (
    SELECT DISTINCT subject
    FROM events
    WHERE subject IS NOT NULL
      AND kind IN ('auth.session.created', 'key.created', 'api_key.created')
      AND COALESCE(data->>'issued_ip', data->>'created_ip', '') IN ('127.0.0.1', '::1')
)
INSERT INTO events (at, actor, kind, subject, data)
SELECT
    now(),
    'migration:20260915120000002',
    'security.client_ip_provenance_cutover',
    affected_accounts.subject,
    jsonb_build_object(
        'historical_client_ip', 'unrecoverable',
        'affected_values', jsonb_build_array('127.0.0.1', '::1'),
        'strategy', 'retain_source_rows_and_treat_legacy_loopback_as_unknown',
        'migration_recorded_at', now(),
        'migration_version', '20260915120000002'
    )
FROM affected_accounts
WHERE NOT EXISTS (
    SELECT 1
    FROM events marker
    WHERE marker.kind = 'security.client_ip_provenance_cutover'
      AND marker.subject = affected_accounts.subject
);
-- +goose StatementEnd

-- +goose Down
DELETE FROM events
WHERE actor = 'migration:20260915120000002'
  AND kind = 'security.client_ip_provenance_cutover';
