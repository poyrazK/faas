-- filename: 20261015173147723_internal_mail_domain_lifecycle.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE internal_mail_domains
    ADD COLUMN IF NOT EXISTS dkim_key_id uuid,
    ADD COLUMN IF NOT EXISTS dns_revision bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS authorization_state text NOT NULL DEFAULT 'unchecked',
    ADD COLUMN IF NOT EXISTS last_checked_at timestamptz,
    ADD COLUMN IF NOT EXISTS last_verified_at timestamptz,
    ADD COLUMN IF NOT EXISTS verified_sending_ip text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS check_result text NOT NULL DEFAULT 'unchecked',
    ADD COLUMN IF NOT EXISTS failure_count integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS first_failure_at timestamptz;
ALTER TABLE internal_mail_recipients ADD COLUMN IF NOT EXISTS signing_selector text NOT NULL DEFAULT '';
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='internal_mail_domains_authorization_check' AND conrelid='internal_mail_domains'::regclass) THEN
        ALTER TABLE internal_mail_domains ADD CONSTRAINT internal_mail_domains_authorization_check
            CHECK (authorization_state IN ('unchecked','valid','failing','paused') AND check_result IN ('unchecked','valid','mismatch','unavailable') AND failure_count BETWEEN 0 AND 2 AND dns_revision>=0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='internal_mail_recipients_selector_check' AND conrelid='internal_mail_recipients'::regclass) THEN
        ALTER TABLE internal_mail_recipients ADD CONSTRAINT internal_mail_recipients_selector_check CHECK (length(signing_selector)<=63);
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS internal_mail_dkim_keys (
    id uuid NOT NULL,
    namespace text NOT NULL,
    domain_id uuid NOT NULL,
    selector text NOT NULL CHECK (selector ~ '^[a-z0-9]{1,63}$'),
    public_key text NOT NULL,
    sealed_key bytea NOT NULL,
    legacy boolean NOT NULL DEFAULT false,
    state text NOT NULL CHECK (state IN ('staged','active','retiring','retired')),
    check_result text NOT NULL DEFAULT 'unchecked' CHECK (check_result IN ('unchecked','valid','mismatch','unavailable')),
    checked_at timestamptz,
    first_verified_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    activated_at timestamptz,
    deactivated_at timestamptz,
    PRIMARY KEY(namespace,id),
    UNIQUE(namespace,domain_id,selector),
    FOREIGN KEY(namespace,domain_id) REFERENCES internal_mail_domains(namespace,id),
    CHECK (NOT legacy OR id=domain_id),
    CHECK (state NOT IN ('retiring','retired') OR deactivated_at IS NOT NULL),
    CHECK (state<>'retired' OR octet_length(sealed_key)=0)
);
CREATE UNIQUE INDEX IF NOT EXISTS internal_mail_dkim_keys_active_idx ON internal_mail_dkim_keys(namespace,domain_id) WHERE state='active';
CREATE UNIQUE INDEX IF NOT EXISTS internal_mail_dkim_keys_staged_idx ON internal_mail_dkim_keys(namespace,domain_id) WHERE state='staged';
INSERT INTO internal_mail_dkim_keys(id,namespace,domain_id,selector,public_key,sealed_key,legacy,state,created_at,activated_at)
SELECT id,namespace,id,selector,public_key,sealed_key,true,'active',created_at,created_at FROM internal_mail_domains
WHERE dkim_key_id IS NULL ON CONFLICT DO NOTHING;

ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Removing key history or authorization pauses would make a downgrade unsafe.
LOCK TABLE internal_mail_domains, internal_mail_recipients, internal_mail_operations IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF to_regclass('internal_mail_dkim_keys') IS NOT NULL THEN
        LOCK TABLE internal_mail_dkim_keys IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_dkim_keys WHERE NOT legacy) THEN
            RAISE EXCEPTION 'internal mail lifecycle cannot roll back after staging signing keys';
        END IF;
    END IF;
    IF EXISTS (SELECT 1 FROM internal_mail_operations WHERE action NOT IN ('reconcile','retention_apply')) THEN
        RAISE EXCEPTION 'internal mail lifecycle cannot discard operator evidence';
    END IF;
    IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='internal_mail_domains'::regclass AND attname='last_checked_at' AND NOT attisdropped) THEN
        IF EXISTS (SELECT 1 FROM internal_mail_domains WHERE last_checked_at IS NOT NULL OR dkim_key_id IS NOT NULL OR authorization_state<>'unchecked') THEN
            RAISE EXCEPTION 'internal mail lifecycle cannot discard authorization state';
        END IF;
    END IF;
END $$;
DROP TABLE IF EXISTS internal_mail_dkim_keys;
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check CHECK (action IN ('reconcile','retention_apply'));
ALTER TABLE internal_mail_recipients DROP CONSTRAINT IF EXISTS internal_mail_recipients_selector_check, DROP COLUMN IF EXISTS signing_selector;
ALTER TABLE internal_mail_domains DROP CONSTRAINT IF EXISTS internal_mail_domains_authorization_check,
    DROP COLUMN IF EXISTS dkim_key_id, DROP COLUMN IF EXISTS dns_revision, DROP COLUMN IF EXISTS authorization_state,
    DROP COLUMN IF EXISTS last_checked_at, DROP COLUMN IF EXISTS last_verified_at,
    DROP COLUMN IF EXISTS verified_sending_ip, DROP COLUMN IF EXISTS check_result,
    DROP COLUMN IF EXISTS failure_count, DROP COLUMN IF EXISTS first_failure_at;
-- +goose StatementEnd
