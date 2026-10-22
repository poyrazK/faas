-- filename: 20261021225219721_database_restore_drills.sql
-- migration-compat: expand

-- ADR-1027: schedd's database scheduler proves each database node's
-- backups restore. Once a day per node it restores one database to a
-- recent point into a throwaway database (drill_id, a database_vms row
-- while the drill runs), records the outcome here and deletes the
-- throwaway database. schedd is the only writer.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS database_restore_drills (
    drill_id     uuid        PRIMARY KEY,
    node_id      text        NOT NULL,
    source_id    uuid        NOT NULL,
    target_at    timestamptz NOT NULL,
    started_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz,
    result       text        NOT NULL DEFAULT 'running',
    recovered_to timestamptz,
    error_code   text,
    CONSTRAINT database_restore_drills_result_check CHECK (result IN ('running', 'ok', 'failed', 'skipped')),
    CONSTRAINT database_restore_drills_finished CHECK ((result = 'running') = (finished_at IS NULL)),
    CONSTRAINT database_restore_drills_error_code CHECK (error_code IS NULL OR error_code ~ '^[a-z0-9_]{1,64}$')
);

CREATE INDEX IF NOT EXISTS database_restore_drills_node_started
    ON database_restore_drills (node_id, started_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS database_restore_drills;
-- +goose StatementEnd
