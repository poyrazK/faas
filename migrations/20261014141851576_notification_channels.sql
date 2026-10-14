-- +goose Up
-- ADR-749: account-level alert notification channels. apid writes the
-- definition columns; meterd writes only the last_* delivery status.
-- Slack URLs and PagerDuty routing keys are sealed (target_sealed); email
-- channels store only the address, which must be the account's own.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS notification_channels (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name text NOT NULL,
    kind text NOT NULL,
    target_sealed bytea,
    target_hint text NOT NULL DEFAULT '',
    pagerduty_region text,
    email text,
    last_delivered_at timestamp with time zone,
    last_error text,
    last_error_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT notification_channels_pkey PRIMARY KEY (id),
    CONSTRAINT notification_channels_account_name_uniq UNIQUE (account_id, name),
    CONSTRAINT notification_channels_name_shape CHECK (name ~ '^[a-z][a-z0-9_-]{0,62}$'),
    CONSTRAINT notification_channels_kind_chk CHECK (
        (kind = 'slack' AND target_sealed IS NOT NULL AND pagerduty_region IS NULL AND email IS NULL)
        OR (kind = 'pagerduty' AND target_sealed IS NOT NULL AND pagerduty_region IN ('us', 'eu') AND email IS NULL)
        OR (kind = 'email' AND target_sealed IS NULL AND pagerduty_region IS NULL AND email IS NOT NULL AND char_length(email) <= 254)),
    CONSTRAINT notification_channels_hint_chk CHECK (char_length(target_hint) <= 64),
    CONSTRAINT notification_channels_error_chk CHECK (last_error IS NULL OR char_length(last_error) <= 256)
);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS notification_channels;
