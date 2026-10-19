-- Customer-owned PostgreSQL database, separate from Gregale's control plane.
CREATE TABLE IF NOT EXISTS gregale_issue_slack_receipts (
 delivery_id text PRIMARY KEY CHECK(octet_length(delivery_id) BETWEEN 1 AND 256),
 transition_sha256 text NOT NULL CHECK(transition_sha256 ~ '^[a-f0-9]{64}$'),
 status text NOT NULL CHECK(status IN ('pending','sent')),
 lease_token uuid NOT NULL,
 lease_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 sent_at timestamptz,
 CHECK((status='pending' AND lease_until IS NOT NULL AND sent_at IS NULL)
    OR (status='sent' AND lease_until IS NULL AND sent_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS gregale_issue_slack_receipts_sent_idx ON gregale_issue_slack_receipts(sent_at) WHERE status='sent';
