-- filename: 20260929184528578_egress_circuit_desired.sql

-- +goose Up
-- ADR-375: schedd is the sole writer. vmmd reads before boot/restore so a
-- parked app, daemon restart or new placement cannot start with empty rules.
CREATE TABLE IF NOT EXISTS app_egress_circuits (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    targets jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(targets) = 'array' AND jsonb_array_length(targets) <= 3200),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS app_egress_circuits;
