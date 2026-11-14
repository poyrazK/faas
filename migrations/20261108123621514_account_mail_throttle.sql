-- migration-compat: expand
-- filename: 20261108123621514_account_mail_throttle.sql

-- +goose Up
-- Per-account counters for security and reminder mail (ADR-20261108160000).
-- Each apid kept its own in-memory counts, so with several apid processes an
-- account could get the per-kind cap once per process. One row per account
-- and mail kind holds a fixed window's count and the last send; a single
-- conditional upsert decides and counts a send. Every step is re-runnable.
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS account_mail_throttle (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    kind text NOT NULL,
    window_started_at timestamptz NOT NULL,
    sent_in_window integer NOT NULL,
    last_sent_at timestamptz NOT NULL,
    CONSTRAINT account_mail_throttle_pkey PRIMARY KEY (account_id, kind),
    CONSTRAINT account_mail_throttle_kind_check CHECK (kind ~ '^[a-z][a-z0-9_]{0,47}$'),
    CONSTRAINT account_mail_throttle_count_check CHECK (sent_in_window BETWEEN 1 AND 1000),
    CONSTRAINT account_mail_throttle_times_check CHECK (
        isfinite(window_started_at) AND isfinite(last_sent_at) AND last_sent_at >= window_started_at)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS account_mail_throttle;
-- +goose StatementEnd
