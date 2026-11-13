-- filename: 20261113170412214_managed_postgres_egress_hold_notices.sql
-- migration-compat: expand
--
-- ADR-20261110160000 amendment 2026-11-13: each managed PostgreSQL database
-- has its own monthly egress allowance, and the cap sweep holds only the
-- database that sent it. The hold email is per database too: one row per
-- database and UTC month, recorded the first time a sweep holds it, with the
-- egress it had sent, the allowance and the hold ceiling in bytes. The email
-- (template managed_postgres_cap) links its intent to the row, so each held
-- database is mailed at most once a month and names the database. apid is
-- the only writer; rows go with the database or its subject.
--
-- The previous release keeps recording one egress_held row per subject in
-- managed_postgres_cap_events; this table is new and it never reads it.
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS managed_postgres_egress_hold_notices (
    database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    period_start timestamptz NOT NULL
        CHECK (period_start = (date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')),
    subject_id uuid NOT NULL REFERENCES resource_namespaces(id) ON DELETE CASCADE,
    reached_at timestamptz NOT NULL CHECK (isfinite(reached_at) AND reached_at >= period_start),
    used_bytes bigint NOT NULL CHECK (used_bytes >= 0),
    allowance_bytes bigint NOT NULL CHECK (allowance_bytes > 0),
    ceiling_bytes bigint NOT NULL CHECK (ceiling_bytes >= allowance_bytes),
    overage_billed boolean NOT NULL,
    mail_intent_id uuid,
    PRIMARY KEY (database_id, period_start)
);
CREATE INDEX IF NOT EXISTS managed_postgres_egress_hold_notices_subject_idx
    ON managed_postgres_egress_hold_notices (subject_id);
COMMENT ON TABLE managed_postgres_egress_hold_notices IS 'Managed PostgreSQL databases held for sending their own monthly egress allowance, one row per database and UTC month; written only by apid (ADR-20261110160000).';

-- +goose Down
DROP TABLE IF EXISTS managed_postgres_egress_hold_notices;
