-- +goose Up
-- +goose StatementBegin
DO $product_control_replay$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='product_control_email_deliveries' AND column_name='unsubscribe_hash') THEN
        ALTER TABLE product_control_email_deliveries
         ADD COLUMN unsubscribe_hash bytea UNIQUE CHECK(unsubscribe_hash IS NULL OR octet_length(unsubscribe_hash)=32),
         ADD COLUMN unsubscribe_sealed bytea NOT NULL DEFAULT '' CHECK(octet_length(unsubscribe_sealed)<=4096),
         ADD CONSTRAINT product_email_unsubscribe_shape CHECK((unsubscribe_hash IS NULL)=(octet_length(unsubscribe_sealed)=0)),
         DROP CONSTRAINT product_control_email_deliveries_reason_check,
         ADD CONSTRAINT product_control_email_deliveries_reason_check CHECK(reason IN ('','access_changed','preferences_changed','source_changed','authorization_unavailable','transport_unavailable','provider_rejected','retry_exhausted','expired','recipient_suppressed','unsubscribed'));
    END IF;
END;
$product_control_replay$;
-- +goose StatementEnd

-- Each opt-out is backed by a retained delivery. Existing delivery caps and
-- pruning bound receipt storage without ever refusing an opt-out for capacity.
CREATE TABLE IF NOT EXISTS product_control_email_opt_outs (
 id uuid PRIMARY KEY CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 subscription_id uuid NOT NULL REFERENCES product_control_email_subscriptions(id) ON DELETE CASCADE,
 delivery_id uuid NOT NULL REFERENCES product_control_email_deliveries(id) ON DELETE CASCADE,
 expected_version bigint NOT NULL CHECK(expected_version BETWEEN 1 AND 1000000),
 version bigint NOT NULL CHECK(version=LEAST(expected_version+1,1000000)),
 generation bigint NOT NULL CHECK(generation BETWEEN 1 AND 1000000),
 unsubscribed_at timestamptz NOT NULL CHECK(isfinite(unsubscribed_at)),
 UNIQUE(subscription_id,expected_version)
);
CREATE INDEX IF NOT EXISTS product_email_opt_out_recent ON product_control_email_opt_outs(subscription_id,unsubscribed_at DESC,id DESC);

-- +goose Down
DROP TABLE product_control_email_opt_outs;
ALTER TABLE product_control_email_deliveries
 DROP CONSTRAINT product_email_unsubscribe_shape,
 DROP COLUMN unsubscribe_hash,
 DROP COLUMN unsubscribe_sealed,
 DROP CONSTRAINT product_control_email_deliveries_reason_check;
UPDATE product_control_email_deliveries SET reason='preferences_changed' WHERE reason='unsubscribed';
ALTER TABLE product_control_email_deliveries
 ADD CONSTRAINT product_control_email_deliveries_reason_check CHECK(reason IN ('','access_changed','preferences_changed','source_changed','authorization_unavailable','transport_unavailable','provider_rejected','retry_exhausted','expired','recipient_suppressed'));
