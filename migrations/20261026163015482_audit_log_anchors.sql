-- filename: 20261026163015482_audit_log_anchors.sql
-- migration-compat: expand
-- ADR-20261026163015: tamper evidence for audit_log. Daemons may only
-- insert audit rows (#5285), but the schema owner can still rewrite or
-- erase them. Each anchor seals the audit rows received in one window
-- [window_start, window_end): their count and a SHA-256 digest over them,
-- chained to the previous anchor by an HMAC whose key lives on the control
-- plane host, never in the database. `gregalectl audit anchor` appends
-- anchors and logs each one off-host; `gregalectl audit verify` recomputes
-- the chain.
--
-- The previous release neither reads nor writes the table.

-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS audit_log_anchors (
    seq          bigint      PRIMARY KEY CHECK (seq > 0),
    window_start timestamptz NOT NULL,
    window_end   timestamptz NOT NULL,
    row_count    bigint      NOT NULL CHECK (row_count >= 0),
    rows_digest  text        NOT NULL CHECK (rows_digest ~ '^[0-9a-f]{64}$'),
    prev_mac     text        NOT NULL CHECK (prev_mac = '' OR prev_mac ~ '^[0-9a-f]{64}$'),
    mac          text        NOT NULL CHECK (mac ~ '^[0-9a-f]{64}$'),
    key_id       text        NOT NULL CHECK (key_id ~ '^[0-9a-f]{16}$'),
    created_at   timestamptz NOT NULL DEFAULT now(),
    CHECK (window_end > window_start),
    CHECK ((seq = 1) = (prev_mac = ''))
);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS audit_log_anchors;
