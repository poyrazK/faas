-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-20261113133500: every abuse recycle (ADR-361 egress fan-out and flood,
-- ADR-20261112120603 sustained CPU) is a strike against the resource owner
-- (an account or an organization, hence no foreign key). schedd counts the
-- strikes inside the escalation window to place the account abuse hold, so
-- the count survives restarts and spans every schedd. Rows older than the
-- window are deleted on each new strike; nothing is kept past it.
CREATE TABLE IF NOT EXISTS account_abuse_strikes (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 account_id uuid NOT NULL,
 reason text NOT NULL CHECK (reason IN ('egress_fanout', 'egress_flood', 'cpu_sustained')),
 app_id uuid,
 struck_at timestamptz NOT NULL CHECK (isfinite(struck_at))
);
CREATE INDEX IF NOT EXISTS account_abuse_strikes_account_idx ON account_abuse_strikes (account_id, struck_at);
CREATE INDEX IF NOT EXISTS account_abuse_strikes_struck_at_idx ON account_abuse_strikes (struck_at);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS account_abuse_strikes;
