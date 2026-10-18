-- filename: 20261018115202786_platform_mail_catalog_deployment_scopes.sql
-- migration-compat: expand
-- Existing APIs and N-1 writers already accept catalog scopes of one or two
-- characters. Widen only the mail checks to the same 1..40 runtime grammar;
-- preserve every revision, receipt, source identity and rejection of malformed scopes.
-- ADR-568: deployment-mail fences must accept every authoritative catalog scope.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE platform_mail_deployment_fences
    DROP CONSTRAINT IF EXISTS platform_mail_deployment_fences_scope_check;
ALTER TABLE platform_mail_deployment_fences
    ADD CONSTRAINT platform_mail_deployment_fences_scope_check
    CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');
ALTER TABLE platform_mail_producer_outbox
    DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_deployment_failure_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_deployment_failure_check CHECK (
    (template='deployment_failed' AND deployment_failure_id IS NOT NULL AND deployment_failure_app_id IS NOT NULL
        AND deployment_failure_scope IS NOT NULL AND deployment_failure_scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$'
        AND deployment_failure_revision IS NOT NULL AND deployment_failure_revision>0
        AND deployment_failure_account_revision IS NOT NULL AND deployment_failure_account_revision>0
        AND deployment_failure_app_revision IS NOT NULL AND deployment_failure_app_revision>0
        AND deployment_failure_success_revision IS NOT NULL AND deployment_failure_success_revision>=0
        AND deployment_failure_at IS NOT NULL AND isfinite(deployment_failure_at))
    OR (template<>'deployment_failed' AND deployment_failure_id IS NULL AND deployment_failure_app_id IS NULL
        AND deployment_failure_scope IS NULL AND deployment_failure_revision IS NULL AND deployment_failure_account_revision IS NULL
        AND deployment_failure_app_revision IS NULL AND deployment_failure_success_revision IS NULL AND deployment_failure_at IS NULL)
);
-- +goose StatementEnd

-- Down refuses accepted short-scope rows atomically; it never discards history.
-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE platform_mail_deployment_fences
    DROP CONSTRAINT IF EXISTS platform_mail_deployment_fences_scope_check;
ALTER TABLE platform_mail_deployment_fences
    ADD CONSTRAINT platform_mail_deployment_fences_scope_check
    CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$');
ALTER TABLE platform_mail_producer_outbox
    DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_deployment_failure_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_deployment_failure_check CHECK (
    (template='deployment_failed' AND deployment_failure_id IS NOT NULL AND deployment_failure_app_id IS NOT NULL
        AND deployment_failure_scope IS NOT NULL AND deployment_failure_scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'
        AND deployment_failure_revision IS NOT NULL AND deployment_failure_revision>0
        AND deployment_failure_account_revision IS NOT NULL AND deployment_failure_account_revision>0
        AND deployment_failure_app_revision IS NOT NULL AND deployment_failure_app_revision>0
        AND deployment_failure_success_revision IS NOT NULL AND deployment_failure_success_revision>=0
        AND deployment_failure_at IS NOT NULL AND isfinite(deployment_failure_at))
    OR (template<>'deployment_failed' AND deployment_failure_id IS NULL AND deployment_failure_app_id IS NULL
        AND deployment_failure_scope IS NULL AND deployment_failure_revision IS NULL AND deployment_failure_account_revision IS NULL
        AND deployment_failure_app_revision IS NULL AND deployment_failure_success_revision IS NULL AND deployment_failure_at IS NULL)
);
-- +goose StatementEnd
