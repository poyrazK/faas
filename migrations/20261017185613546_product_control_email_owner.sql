-- +goose Up
SET LOCAL lock_timeout = '5s';
-- At most one receipt for each failed attempt; the unchanged six-attempt budget
-- bounds these records. Receipts expire with the retained delivery.
CREATE TABLE IF NOT EXISTS product_control_email_retry_receipts (
 delivery_id uuid NOT NULL REFERENCES product_control_email_deliveries(id) ON DELETE CASCADE,
 failed_attempt integer NOT NULL CHECK(failed_attempt BETWEEN 1 AND 5),
 queued_at timestamptz NOT NULL CHECK(isfinite(queued_at)),
 PRIMARY KEY(delivery_id,failed_attempt)
);
CREATE INDEX IF NOT EXISTS product_control_email_owner_scope ON product_control_email_subscriptions(account_id,app_id,tenant_id,id);

-- +goose Down
DROP INDEX product_control_email_owner_scope;
DROP TABLE product_control_email_retry_receipts;
