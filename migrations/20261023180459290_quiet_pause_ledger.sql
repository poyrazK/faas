-- filename: 20261023180459290_quiet_pause_ledger.sql
-- migration-compat: expand
-- ADR-20261023171905: the quiet pause ledger. schedd records the quiet pauses
-- vmmd reports, and how far each node's record is complete; meterd subtracts
-- the paused seconds it can prove from billed residency. New tables only:
-- nothing running reads or writes them until schedd's quiet_pause_ledger and
-- meterd's quiet_pause_unbilled switches are on.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS instance_quiet_pauses (
    instance_id uuid NOT NULL,
    node_id uuid NOT NULL,
    started_at timestamptz NOT NULL,
    -- NULL while the pause is open; billing then counts it only up to the
    -- node's watermark.
    ended_at timestamptz,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (instance_id, started_at),
    CONSTRAINT instance_quiet_pauses_order CHECK (ended_at IS NULL OR ended_at >= started_at)
);
-- meterd reads the pauses overlapping one closed minute; retention deletes
-- by age.
CREATE INDEX IF NOT EXISTS instance_quiet_pauses_started_at ON instance_quiet_pauses (started_at);
-- schedd reloads a node's open pauses after a restart.
CREATE INDEX IF NOT EXISTS instance_quiet_pauses_open ON instance_quiet_pauses (node_id) WHERE ended_at IS NULL;

-- covered_until is the newest node report whose pauses are all recorded.
-- meterd trusts the ledger for a minute only once it has passed the minute's
-- end; otherwise it bills the minute in full.
CREATE TABLE IF NOT EXISTS quiet_pause_watermarks (
    node_id uuid PRIMARY KEY,
    covered_until timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS quiet_pause_watermarks;
DROP TABLE IF EXISTS instance_quiet_pauses;
-- +goose StatementEnd
