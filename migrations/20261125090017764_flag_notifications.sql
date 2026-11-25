-- filename: 20261125090017764_flag_notifications.sql
-- migration-compat: expand
-- ADR-20261125085913: flag notifications. flag_notification_settings keeps
-- an environment's notification channels for its experiments and flag
-- lifecycle; flag_experiments.notify_channel_ids an experiment's own.
-- flag_notifications is the outbox apid queues experiment decisions and
-- problems, weekly lifecycle digests and flag expiries into (one row per
-- unique key); meterd claims due rows, sends them to the captured channels
-- and records the outcome. The tables are new and the column has a
-- default, so the previous release neither reads nor writes them. The
-- experiment CHECK is added NOT VALID and validated. Re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS flag_notification_settings (
    environment_id uuid PRIMARY KEY REFERENCES project_environments(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    channel_ids uuid[] NOT NULL DEFAULT '{}',
    updated_by text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT flag_notification_settings_chk CHECK (
        cardinality(channel_ids) <= 5
        AND length(updated_by) BETWEEN 1 AND 256)
);
COMMENT ON TABLE flag_notification_settings IS 'Notification channels for an environment''s experiments and flag lifecycle (ADR-20261125085913).';

CREATE TABLE IF NOT EXISTS flag_notifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
    kind text NOT NULL,
    key text NOT NULL,
    subject text NOT NULL,
    summary text NOT NULL,
    channel_ids uuid[] NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    settled_at timestamptz,
    CONSTRAINT flag_notifications_key_uniq UNIQUE (key),
    CONSTRAINT flag_notifications_chk CHECK (
        kind IN ('experiment_decision', 'experiment_problem', 'lifecycle_digest', 'flag_expired')
        AND length(key) BETWEEN 1 AND 256
        AND length(subject) BETWEEN 1 AND 256
        AND length(summary) BETWEEN 1 AND 8192
        AND cardinality(channel_ids) BETWEEN 1 AND 5
        AND status IN ('pending', 'delivered', 'abandoned')
        AND attempts >= 0
        AND length(last_error) <= 512
        AND ((status = 'pending') = (settled_at IS NULL)))
);
COMMENT ON TABLE flag_notifications IS 'Outbox of flag notifications queued by apid and delivered by meterd (ADR-20261125085913).';

CREATE INDEX IF NOT EXISTS flag_notifications_due_idx
    ON flag_notifications (next_attempt_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS flag_notifications_settled_idx
    ON flag_notifications (settled_at) WHERE status <> 'pending';

ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS notify_channel_ids uuid[] NOT NULL DEFAULT '{}';

-- +goose StatementBegin
DO $notify$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'flag_experiments'::regclass AND conname = 'flag_experiments_notify_chk') THEN
    ALTER TABLE flag_experiments ADD CONSTRAINT flag_experiments_notify_chk CHECK (cardinality(notify_channel_ids) <= 5) NOT VALID;
  END IF;
END
$notify$;
-- +goose StatementEnd

ALTER TABLE flag_experiments VALIDATE CONSTRAINT flag_experiments_notify_chk;

-- +goose Down
SET lock_timeout = '5s';
ALTER TABLE flag_experiments DROP CONSTRAINT IF EXISTS flag_experiments_notify_chk;
ALTER TABLE flag_experiments DROP COLUMN IF EXISTS notify_channel_ids;
DROP TABLE IF EXISTS flag_notifications;
DROP TABLE IF EXISTS flag_notification_settings;
