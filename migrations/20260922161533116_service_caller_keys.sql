-- filename: 20260922161533116_service_caller_keys.sql
-- +goose Up
-- +goose StatementBegin

-- ADR-206 caller assertions are minted by the node local to the CALLER, but
-- verified by a workload running on the TARGET's node. Those are frequently
-- different boxes, so a node cannot verify with only the key it generated
-- itself: it has to publish its own public half and read every peer's.
--
-- Deliberately NOT compute_node_keys (ADR-053). That table would accept the
-- shape, but it is vmmd's: vmmd writes it, its keys are ECDSA-P-256, and it
-- is scoped to CapacityReport signing. A second writer with a different
-- algorithm and a different meaning for the same rows would blur an ownership
-- boundary the platform keeps sharp elsewhere.
--
-- Public halves only. The private key never leaves the node that generated it.
CREATE TABLE IF NOT EXISTS service_caller_keys (
    node_id         text PRIMARY KEY,
    key_id          text NOT NULL,
    public_key_pem  text NOT NULL,
    created_at      timestamp with time zone NOT NULL DEFAULT now(),
    rotated_at      timestamp with time zone,
    CONSTRAINT service_caller_keys_key_id_shape
        CHECK (key_id ~ '^[A-Za-z0-9_-]{16,64}$'),
    CONSTRAINT service_caller_keys_pem_shape
        CHECK (public_key_pem LIKE '-----BEGIN PUBLIC KEY-----%')
);

-- A verifier resolves kid -> key on the request path, so that lookup must not
-- scan. node_id is the primary key because a node publishes exactly one
-- current key; rotation replaces the row rather than accumulating history.
CREATE UNIQUE INDEX IF NOT EXISTS service_caller_keys_key_id_idx
    ON service_caller_keys (key_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.service_caller_keys_key_id_idx;
DROP TABLE IF EXISTS public.service_caller_keys;
-- +goose StatementEnd
