-- filename: 20261016181202093_internal_mail_recovery.sql

-- +goose Up
-- +goose StatementBegin
-- Restoring PostgreSQL cannot detect its own rewind. A fresh operator UUID,
-- supplied outside the restored snapshot before startup, installs this hold.
-- Generation mismatch quarantines old pending mail without rewriting custody.
CREATE TABLE IF NOT EXISTS internal_mail_recovery_runs (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    recovery_id uuid NOT NULL CHECK (recovery_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    PRIMARY KEY(namespace,recovery_id)
);
CREATE TABLE IF NOT EXISTS internal_mail_recovery_state (
    namespace text PRIMARY KEY,
    recovery_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    held boolean NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(updated_at)),
    FOREIGN KEY(namespace,recovery_id) REFERENCES internal_mail_recovery_runs(namespace,recovery_id)
);
ALTER TABLE internal_mail_recipients ADD COLUMN IF NOT EXISTS recovery_id uuid NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000'::uuid;
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel','reputation_release','event_feed_replay',
                      'producer_rotate','producer_revoke','producer_suspend','producer_resume',
                      'encryption_activate','encryption_rewrap','encryption_retire','suppression_add','suppression_release','recovery_start','recovery_review','recovery_release'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
LOCK TABLE internal_mail_recipients,internal_mail_operations IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables
        WHERE table_schema=current_schema() AND table_name='internal_mail_recovery_state') THEN
        LOCK TABLE internal_mail_recovery_state IN ACCESS EXCLUSIVE MODE;
        LOCK TABLE internal_mail_recovery_runs IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_recovery_state) OR EXISTS (SELECT 1 FROM internal_mail_recovery_runs) THEN
            RAISE EXCEPTION 'internal mail cannot discard recovery hold or generation history';
        END IF;
    END IF;
    IF EXISTS (SELECT 1 FROM internal_mail_recipients WHERE recovery_id<>'00000000-0000-0000-0000-000000000000'::uuid)
        OR EXISTS (SELECT 1 FROM internal_mail_operations WHERE action IN ('recovery_start','recovery_review','recovery_release')) THEN
        RAISE EXCEPTION 'internal mail cannot discard recovery quarantine or audit';
    END IF;
END $$;
DROP TABLE IF EXISTS internal_mail_recovery_state;
DROP TABLE IF EXISTS internal_mail_recovery_runs;
ALTER TABLE internal_mail_recipients DROP COLUMN IF EXISTS recovery_id;
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel','reputation_release','event_feed_replay',
                      'producer_rotate','producer_revoke','producer_suspend','producer_resume',
                      'encryption_activate','encryption_rewrap','encryption_retire','suppression_add','suppression_release'));
-- +goose StatementEnd
