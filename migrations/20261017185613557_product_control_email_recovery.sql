-- +goose Up
-- +goose StatementBegin
DO $product_control_replay$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='product_control_email_subscriptions' AND column_name='recipient_generation') THEN
        ALTER TABLE product_control_email_subscriptions
         ADD COLUMN recipient_generation bigint NOT NULL DEFAULT 1 CHECK(recipient_generation BETWEEN 1 AND 1000000),
         ADD COLUMN pause_recipient_sealed bytea NOT NULL DEFAULT '' CHECK(octet_length(pause_recipient_sealed)<=4096),
         ADD CONSTRAINT product_email_pause_recipient_shape CHECK(pause_reason<>'' OR octet_length(pause_recipient_sealed)=0);
    END IF;
END;
$product_control_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $product_control_replay$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='product_control_email_deliveries' AND column_name='recipient_generation') THEN
        ALTER TABLE product_control_email_deliveries
         ADD COLUMN recipient_generation bigint NOT NULL DEFAULT 1 CHECK(recipient_generation BETWEEN 1 AND 1000000),
         ADD COLUMN recipient_sealed bytea NOT NULL DEFAULT '' CHECK(octet_length(recipient_sealed)<=4096);
    END IF;
END;
$product_control_replay$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS product_control_email_recoveries (
 id uuid PRIMARY KEY CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 subscription_id uuid NOT NULL REFERENCES product_control_email_subscriptions(id) ON DELETE CASCADE,
 expected_version bigint NOT NULL CHECK(expected_version BETWEEN 1 AND 999999),
 previous_generation bigint NOT NULL CHECK(previous_generation BETWEEN 1 AND 999999),
 version bigint NOT NULL CHECK(version=expected_version+1),
 generation bigint NOT NULL CHECK(generation=previous_generation+1),
 work_outcomes boolean NOT NULL,
 help_replies boolean NOT NULL,
 recovered_at timestamptz NOT NULL CHECK(isfinite(recovered_at)),
 CONSTRAINT product_email_recovery_consent CHECK(work_outcomes OR help_replies),
 UNIQUE(subscription_id,previous_generation)
);
CREATE INDEX IF NOT EXISTS product_email_recovery_recent ON product_control_email_recoveries(subscription_id,recovered_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS product_email_recovery_expiry ON product_control_email_recoveries(recovered_at,id);

-- +goose Down
DROP TABLE product_control_email_recoveries;
ALTER TABLE product_control_email_deliveries DROP COLUMN recipient_generation,DROP COLUMN recipient_sealed;
ALTER TABLE product_control_email_subscriptions DROP CONSTRAINT product_email_pause_recipient_shape,DROP COLUMN recipient_generation,DROP COLUMN pause_recipient_sealed;
