-- ADR-843: transport acceptance only. Entity state/pending work stay in the bucket.
-- +goose Up
CREATE TABLE IF NOT EXISTS entity_outbox_acceptances (
    message_id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT now()
);
-- No delivery/webhook FK and no TTL: pruning terminal delivery history or
-- deleting a destination must not re-enable a delayed acceptance retry.

-- +goose Down
DROP TABLE IF EXISTS entity_outbox_acceptances;
