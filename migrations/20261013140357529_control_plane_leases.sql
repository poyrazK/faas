-- filename: 20261013140357529_control_plane_leases.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-1237 §4: one control-plane singleton (central schedd work, meterd, ...)
-- runs on exactly one of the cell's control nodes. The holder renews its row
-- before expires_at; another node takes over only after the row expired by
-- the database clock. epoch comes from one sequence and changes on every
-- takeover, so work guarded by an older epoch can be refused.
CREATE SEQUENCE IF NOT EXISTS control_plane_lease_epoch_seq;

CREATE TABLE IF NOT EXISTS control_plane_leases (
    name text PRIMARY KEY CHECK (name <> ''),
    holder text NOT NULL CHECK (holder <> ''),
    epoch bigint NOT NULL CHECK (epoch > 0),
    acquired_at timestamptz NOT NULL,
    renewed_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CONSTRAINT control_plane_leases_expiry CHECK (expires_at > renewed_at)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS control_plane_leases;
DROP SEQUENCE IF EXISTS control_plane_lease_epoch_seq;
-- +goose StatementEnd
