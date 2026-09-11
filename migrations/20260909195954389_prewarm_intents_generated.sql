-- filename: 20260909195954389_prewarm_intents_generated.sql
-- +goose Up
-- +goose StatementBegin
-- Durable customer intent for scheduled capacity restoration. The row is
-- separate from apps.min_instances because it is temporary and must not turn
-- a one-off demand window into a permanent billed floor.
CREATE TABLE IF NOT EXISTS prewarm_intents (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    app_id uuid NOT NULL,
    account_id uuid NOT NULL,
    count integer NOT NULL,
    wake_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    trigger text NOT NULL DEFAULT 'calendar',
    status text NOT NULL DEFAULT 'pending',
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    claimed_at timestamp with time zone,
    fired_at timestamp with time zone,
    admitted_count integer NOT NULL DEFAULT 0,
    outcome text NOT NULL DEFAULT '',
    last_error text NOT NULL DEFAULT '',
    CONSTRAINT prewarm_intents_pkey PRIMARY KEY (id),
    CONSTRAINT prewarm_intents_app_id_fkey FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE,
    CONSTRAINT prewarm_intents_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT prewarm_intents_count_chk CHECK (count > 0),
    CONSTRAINT prewarm_intents_window_chk CHECK (expires_at > wake_at),
    CONSTRAINT prewarm_intents_trigger_chk CHECK (trigger IN ('calendar', 'cron', 'pattern', 'webhook')),
    CONSTRAINT prewarm_intents_status_chk CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled'))
);

CREATE INDEX IF NOT EXISTS prewarm_intents_due_idx
    ON prewarm_intents (wake_at, created_at)
    WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS prewarm_intents_app_idx
    ON prewarm_intents (app_id, wake_at DESC, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS prewarm_intents;
-- +goose StatementEnd
