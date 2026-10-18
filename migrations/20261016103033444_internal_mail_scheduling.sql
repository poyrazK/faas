-- filename: 20261016103033444_internal_mail_scheduling.sql

-- +goose Up
-- +goose StatementBegin
-- Timing is durable metadata. No feature gate or delivery path is activated.
ALTER TABLE internal_mail_messages ADD COLUMN IF NOT EXISTS send_at timestamptz;
ALTER TABLE internal_mail_messages ADD COLUMN IF NOT EXISTS expires_at timestamptz;
ALTER TABLE internal_mail_messages DROP CONSTRAINT IF EXISTS internal_mail_messages_timing_check;
ALTER TABLE internal_mail_messages ADD CONSTRAINT internal_mail_messages_timing_check
    CHECK ((send_at IS NULL OR (isfinite(send_at) AND send_at>=created_at))
       AND (expires_at IS NULL OR (isfinite(expires_at) AND expires_at>coalesce(send_at,created_at))));
ALTER TABLE internal_mail_recipients DROP CONSTRAINT IF EXISTS internal_mail_recipients_status_check;
ALTER TABLE internal_mail_recipients ADD CONSTRAINT internal_mail_recipients_status_check
    CHECK (status IN ('queued','claimed','submitting','submitted','accepted','bounced','complained','suppressed','failed','uncertain','deferred','cancelled','expired'));
ALTER TABLE internal_mail_events DROP CONSTRAINT IF EXISTS internal_mail_events_kind_check;
ALTER TABLE internal_mail_events ADD CONSTRAINT internal_mail_events_kind_check
    CHECK (kind IN ('queued','submitted','accepted','bounced','complained','suppressed','failed','uncertain','deferred','retry','cancelled','expired'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    LOCK TABLE internal_mail_messages,internal_mail_recipients,internal_mail_events IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema()
        AND table_name='internal_mail_messages' AND column_name='send_at') THEN
        IF EXISTS (SELECT 1 FROM internal_mail_messages WHERE send_at IS NOT NULL OR expires_at IS NOT NULL) THEN
            RAISE EXCEPTION 'internal mail cannot discard durable scheduling or expiry';
        END IF;
    END IF;
    IF EXISTS (SELECT 1 FROM internal_mail_recipients WHERE status='expired')
       OR EXISTS (SELECT 1 FROM internal_mail_events WHERE kind='expired') THEN
        RAISE EXCEPTION 'internal mail cannot discard expiry evidence';
    END IF;
END $$;
ALTER TABLE internal_mail_messages DROP CONSTRAINT IF EXISTS internal_mail_messages_timing_check;
ALTER TABLE internal_mail_messages DROP COLUMN IF EXISTS expires_at;
ALTER TABLE internal_mail_messages DROP COLUMN IF EXISTS send_at;
ALTER TABLE internal_mail_recipients DROP CONSTRAINT IF EXISTS internal_mail_recipients_status_check;
ALTER TABLE internal_mail_recipients ADD CONSTRAINT internal_mail_recipients_status_check
    CHECK (status IN ('queued','claimed','submitting','submitted','accepted','bounced','complained','suppressed','failed','uncertain','deferred','cancelled'));
ALTER TABLE internal_mail_events DROP CONSTRAINT IF EXISTS internal_mail_events_kind_check;
ALTER TABLE internal_mail_events ADD CONSTRAINT internal_mail_events_kind_check
    CHECK (kind IN ('queued','submitted','accepted','bounced','complained','suppressed','failed','uncertain','deferred','retry','cancelled'));
-- +goose StatementEnd
