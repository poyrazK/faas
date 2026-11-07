-- migration-compat: expand

-- +goose Up
-- The foreign key locks agent_approval_requests briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261107140000 §5: one row per new agent approval request, telling
-- people about it. apid inserts it in the request's transaction, with the
-- agent's name and review link; meterd claims due rows, emails the
-- recipients and records the outcome.
CREATE TABLE IF NOT EXISTS agent_approval_notifications (
    approval_id  uuid PRIMARY KEY REFERENCES agent_approval_requests(id) ON DELETE CASCADE,
    account_id   uuid NOT NULL,
    org_id       uuid,
    key_id       uuid NOT NULL,
    agent_name   text NOT NULL CHECK (char_length(agent_name) BETWEEN 1 AND 200),
    review_url   text NOT NULL CHECK (char_length(review_url) BETWEEN 1 AND 2048),
    state        text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'sent', 'skipped', 'failed')),
    attempts     integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 10),
    available_at timestamptz NOT NULL DEFAULT now(),
    lease_until  timestamptz,
    finished_at  timestamptz,
    last_error   text CHECK (last_error IS NULL OR char_length(last_error) <= 256),
    created_at   timestamptz NOT NULL DEFAULT now(),
    CHECK ((state = 'pending') = (finished_at IS NULL))
);
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS agent_approval_notifications_due_idx
    ON agent_approval_notifications (available_at) WHERE state = 'pending';
-- +goose StatementBegin
-- ADR-20261107140000 §2: when meterd last emailed a person about an agent
-- key's requests, so it emails them at most once per key every 10 minutes.
CREATE TABLE IF NOT EXISTS agent_approval_email_log (
    recipient_account_id uuid NOT NULL,
    key_id               uuid NOT NULL,
    last_sent_at         timestamptz NOT NULL,
    PRIMARY KEY (recipient_account_id, key_id)
);
-- +goose StatementEnd
COMMENT ON TABLE agent_approval_notifications IS
    'ADR-20261107140000 agent approval requests to tell people about; apid inserts, meterd delivers.';
COMMENT ON TABLE agent_approval_email_log IS
    'ADR-20261107140000 when meterd last emailed a recipient about an agent key; meterd is the only writer.';

-- +goose Down
DROP TABLE IF EXISTS agent_approval_email_log;
DROP TABLE IF EXISTS agent_approval_notifications;
