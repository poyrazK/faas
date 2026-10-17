-- filename: 20261016152534774_internal_mail_producer_lifecycle.sql

-- +goose Up
-- +goose StatementBegin
-- Credential identifiers are one-way, producer-bound references. Bearer
-- secrets remain in private configuration, never in these tables or audits.
CREATE TABLE IF NOT EXISTS internal_mail_producer_access (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    producer_id text NOT NULL CHECK (producer_id ~ '^[a-z][a-z0-9_-]{0,31}$'),
    revision bigint NOT NULL DEFAULT 0 CHECK (revision>=0),
    suspended boolean NOT NULL DEFAULT false,
    active_credential_id text NOT NULL DEFAULT '' CHECK (active_credential_id='' OR active_credential_id ~ '^[0-9a-f]{64}$'),
    request_id uuid,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(updated_at)),
    PRIMARY KEY(namespace,producer_id)
);
CREATE TABLE IF NOT EXISTS internal_mail_producer_credentials (
    namespace text NOT NULL,
    producer_id text NOT NULL,
    credential_id text NOT NULL CHECK (credential_id ~ '^[0-9a-f]{64}$'),
    retires_at timestamptz CHECK (isfinite(retires_at)),
    revoked_at timestamptz CHECK (isfinite(revoked_at)),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    PRIMARY KEY(namespace,producer_id,credential_id),
    FOREIGN KEY(namespace,producer_id) REFERENCES internal_mail_producer_access(namespace,producer_id)
);
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel','reputation_release','event_feed_replay',
                      'producer_rotate','producer_revoke','producer_suspend','producer_resume'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables
        WHERE table_schema=current_schema() AND table_name='internal_mail_producer_access') THEN
        LOCK TABLE internal_mail_producer_access IN ACCESS EXCLUSIVE MODE;
        LOCK TABLE internal_mail_producer_credentials IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_producer_access)
            OR EXISTS (SELECT 1 FROM internal_mail_producer_credentials) THEN
            RAISE EXCEPTION 'internal mail cannot discard producer credential or suspension state';
        END IF;
    END IF;
    LOCK TABLE internal_mail_operations IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM internal_mail_operations WHERE action IN
        ('producer_rotate','producer_revoke','producer_suspend','producer_resume')) THEN
        RAISE EXCEPTION 'internal mail cannot discard producer lifecycle audit';
    END IF;
END $$;
DROP TABLE IF EXISTS internal_mail_producer_credentials;
DROP TABLE IF EXISTS internal_mail_producer_access;
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel','reputation_release','event_feed_replay'));
-- +goose StatementEnd
