-- filename: 20261018162329975_platform_mail_deployment_scope_shape.sql
-- Deployment scopes are 1-40 characters (deployments_scope_shape). The mail
-- fence and failure outbox required at least three, so the deployment mail
-- trigger rejected every deployment in a one- or two-character scope.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE platform_mail_deployment_fences DROP CONSTRAINT platform_mail_deployment_fences_scope_check;
ALTER TABLE platform_mail_deployment_fences ADD CONSTRAINT platform_mail_deployment_fences_scope_check
    CHECK(scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT platform_mail_producer_outbox_deployment_failure_check;
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

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM platform_mail_deployment_fences WHERE length(scope)<3)
        OR EXISTS(SELECT 1 FROM platform_mail_producer_outbox WHERE length(deployment_failure_scope)<3) THEN
        RAISE EXCEPTION 'short deployment scopes prevent downgrade';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE platform_mail_deployment_fences DROP CONSTRAINT platform_mail_deployment_fences_scope_check;
ALTER TABLE platform_mail_deployment_fences ADD CONSTRAINT platform_mail_deployment_fences_scope_check
    CHECK(scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$');
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT platform_mail_producer_outbox_deployment_failure_check;
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
