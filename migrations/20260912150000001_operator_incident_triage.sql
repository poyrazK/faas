-- filename: 20260912150000001_operator_incident_triage.sql

-- +goose Up
-- +goose StatementBegin
-- Durable operator workflow metadata for the observability incident inbox.
-- This table is intentionally independent from deployment/job/node state: a
-- triage update must never add work to the customer deployment hot path.
CREATE TABLE IF NOT EXISTS operator_incident_triage (
    dedupe_key text PRIMARY KEY,
    status text NOT NULL DEFAULT 'open'
           CHECK (status IN ('open', 'acknowledged', 'in_progress', 'resolved')),
    owner text NOT NULL DEFAULT '',
    note text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by text NOT NULL,
    CONSTRAINT operator_incident_triage_owner_len CHECK (length(owner) <= 128),
    CONSTRAINT operator_incident_triage_note_len CHECK (length(note) <= 1024),
    CONSTRAINT operator_incident_triage_updated_by_len CHECK (length(updated_by) <= 128)
);

CREATE INDEX IF NOT EXISTS operator_incident_triage_updated_idx
    ON operator_incident_triage (updated_at DESC, dedupe_key);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS operator_incident_triage;
-- +goose StatementEnd
