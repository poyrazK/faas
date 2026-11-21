-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Account health captured when a customer opens a support ticket
-- (ADR-20261112210000). Identifiers and customer-visible diagnosis only; no
-- conversation text. Deleted with the ticket.
CREATE TABLE IF NOT EXISTS support_ticket_snapshots (
 ticket_id uuid PRIMARY KEY REFERENCES support_tickets(id) ON DELETE CASCADE,
 captured_at timestamptz NOT NULL CHECK(isfinite(captured_at)),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=32768)
);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS support_ticket_snapshots;
