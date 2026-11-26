-- Private application requests, created only after verified completed cleanup.
CREATE TABLE sdk_csv_refund_requests (
 id uuid PRIMARY KEY,
 owner_id text NOT NULL,
 payment_id text NOT NULL REFERENCES sdk_csv_payments(payment_id),
 amount_minor bigint NOT NULL CHECK(amount_minor > 0),
 currency text NOT NULL,
 recovery_checkpoint jsonb NOT NULL,
 enabled boolean NOT NULL DEFAULT true
);
