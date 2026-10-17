-- filename: 20261016183431589_platform_mail_producer_outbox.sql

-- +goose Up
-- +goose StatementBegin
-- apid owns this durable notification intent and handoff state. maild has
-- metadata-only read access through its existing private operator routes.
CREATE TABLE IF NOT EXISTS platform_mail_producer_outbox (
    id uuid PRIMARY KEY CHECK (id<>'00000000-0000-0000-0000-000000000000'::uuid),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    producer_id text NOT NULL CHECK (producer_id ~ '^[a-z][a-z0-9_-]{0,31}$'),
    from_address text NOT NULL CHECK (length(from_address) BETWEEN 3 AND 1024),
    recipient_address text NOT NULL CHECK (length(recipient_address)<=254 AND position(chr(10) in recipient_address)=0 AND position(chr(13) in recipient_address)=0),
    template text NOT NULL CHECK (template='account_deletion_pending'),
    template_version integer NOT NULL CHECK (template_version=1),
    deletion_requested_at timestamptz NOT NULL CHECK (isfinite(deletion_requested_at)),
    restore_until timestamptz NOT NULL CHECK (isfinite(restore_until) AND restore_until>deletion_requested_at),
    expires_at timestamptz NOT NULL CHECK (isfinite(expires_at) AND expires_at>deletion_requested_at),
    recovery_id uuid NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000'::uuid,
    state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','claimed','admitted','failed','expired','cancelled','suppressed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts>=0),
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(next_attempt_at)),
    lease_id uuid,
    lease_until timestamptz,
    code text NOT NULL DEFAULT '' CHECK (code IN ('','maild_unavailable','maild_rejected','attempt_limit','notification_expired','account_changed','platform_suppressed')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(updated_at)),
    UNIQUE(account_id,template,deletion_requested_at),
    CHECK ((state='claimed')=(lease_id IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK (lease_until IS NULL OR isfinite(lease_until)),
    CHECK ((state IN ('queued','claimed'))=(recipient_address<>''))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_ready_idx
    ON platform_mail_producer_outbox(namespace,producer_id,next_attempt_at,id) WHERE state IN ('queued','claimed');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
LOCK TABLE platform_mail_producer_outbox IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM platform_mail_producer_outbox) THEN
        RAISE EXCEPTION 'cannot discard producer mail notification intent or handoff history';
    END IF;
END $$;
DROP TABLE IF EXISTS platform_mail_producer_outbox;
-- +goose StatementEnd
