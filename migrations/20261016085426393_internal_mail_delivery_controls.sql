-- filename: 20261016085426393_internal_mail_delivery_controls.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS internal_mail_delivery_controls (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    scope_id uuid NOT NULL,
    domain_id uuid,
    mode text NOT NULL CHECK (mode IN ('running','paused','draining')),
    revision bigint NOT NULL CHECK (revision>0),
    request_id uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(namespace,scope_id),
    FOREIGN KEY(namespace,domain_id) REFERENCES internal_mail_domains(namespace,id),
    CHECK ((domain_id IS NULL AND scope_id='00000000-0000-0000-0000-000000000000'::uuid)
        OR (domain_id IS NOT NULL AND scope_id=domain_id AND scope_id<>'00000000-0000-0000-0000-000000000000'::uuid))
);
ALTER TABLE internal_mail_recipients DROP CONSTRAINT IF EXISTS internal_mail_recipients_status_check;
ALTER TABLE internal_mail_recipients ADD CONSTRAINT internal_mail_recipients_status_check
    CHECK (status IN ('queued','claimed','submitting','submitted','accepted','bounced','complained','suppressed','failed','uncertain','deferred','cancelled'));
ALTER TABLE internal_mail_events DROP CONSTRAINT IF EXISTS internal_mail_events_kind_check;
ALTER TABLE internal_mail_events ADD CONSTRAINT internal_mail_events_kind_check
    CHECK (kind IN ('queued','submitted','accepted','bounced','complained','suppressed','failed','uncertain','deferred','retry','cancelled'));
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Removing a durable hold or a cancellation would bypass operator intent.
DO $$
BEGIN
    IF to_regclass('internal_mail_delivery_controls') IS NOT NULL THEN
        LOCK TABLE internal_mail_delivery_controls IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_delivery_controls WHERE mode<>'running') THEN
            RAISE EXCEPTION 'internal mail controls cannot roll back with durable delivery holds';
        END IF;
    END IF;
    LOCK TABLE internal_mail_recipients,internal_mail_events,internal_mail_operations IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM internal_mail_recipients WHERE status='cancelled')
       OR EXISTS (SELECT 1 FROM internal_mail_events WHERE kind='cancelled')
       OR EXISTS (SELECT 1 FROM internal_mail_operations WHERE action IN ('delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel')) THEN
        RAISE EXCEPTION 'internal mail controls cannot discard cancellation or operator evidence';
    END IF;
END $$;
DROP TABLE IF EXISTS internal_mail_delivery_controls;
ALTER TABLE internal_mail_recipients DROP CONSTRAINT IF EXISTS internal_mail_recipients_status_check;
ALTER TABLE internal_mail_recipients ADD CONSTRAINT internal_mail_recipients_status_check
    CHECK (status IN ('queued','claimed','submitting','submitted','accepted','bounced','complained','suppressed','failed','uncertain','deferred'));
ALTER TABLE internal_mail_events DROP CONSTRAINT IF EXISTS internal_mail_events_kind_check;
ALTER TABLE internal_mail_events ADD CONSTRAINT internal_mail_events_kind_check
    CHECK (kind IN ('queued','submitted','accepted','bounced','complained','suppressed','failed','uncertain','deferred','retry'));
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify'));
-- +goose StatementEnd
