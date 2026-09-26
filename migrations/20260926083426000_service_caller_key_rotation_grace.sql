-- filename: 20260926083426000_service_caller_key_rotation_grace.sql
-- +goose Up
-- +goose StatementBegin

-- Keep a node's previous caller-assertion key trusted for the maximum token
-- lifetime after rotation. Otherwise a verifier refreshing the key set during
-- a rolling restart can reject a request that was signed just before rotation.
CREATE TABLE IF NOT EXISTS service_caller_key_history (
    key_id         text PRIMARY KEY,
    node_id        text NOT NULL,
    public_key_pem text NOT NULL,
    retire_after   timestamp with time zone NOT NULL,
    CONSTRAINT service_caller_key_history_key_id_shape
        CHECK (key_id ~ '^[A-Za-z0-9_-]{16,64}$'),
    CONSTRAINT service_caller_key_history_pem_shape
        CHECK (public_key_pem LIKE '-----BEGIN PUBLIC KEY-----%')
);

CREATE INDEX IF NOT EXISTS service_caller_key_history_retire_after_idx
    ON service_caller_key_history (retire_after);

COMMENT ON TABLE service_caller_key_history IS
    'Retired service-caller verification keys retained only through the 30-second assertion TTL.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.service_caller_key_history;
-- +goose StatementEnd
