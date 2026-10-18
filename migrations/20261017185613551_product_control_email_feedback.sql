-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $product_control_replay$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='product_control_email_subscriptions' AND column_name='pause_reason') THEN
        ALTER TABLE product_control_email_subscriptions
         ADD COLUMN pause_reason text NOT NULL DEFAULT '' CHECK(pause_reason IN ('','hard_bounce','complaint')),
         ADD COLUMN paused_at timestamptz CHECK(paused_at IS NULL OR isfinite(paused_at)),
         ADD CONSTRAINT product_email_pause_shape CHECK((pause_reason='')=(paused_at IS NULL));
    END IF;
END;
$product_control_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $product_control_replay$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='product_control_email_deliveries' AND column_name='provider_id') THEN
        ALTER TABLE product_control_email_deliveries
         DROP CONSTRAINT product_control_email_deliveries_status_check,
         DROP CONSTRAINT product_control_email_deliveries_reason_check,
         ADD CONSTRAINT product_control_email_deliveries_status_check CHECK(status IN ('queued','leased','captured','provider_accepted','delivered','bounced','complained','failed','cancelled')),
         ADD CONSTRAINT product_control_email_deliveries_reason_check CHECK(reason IN ('','access_changed','preferences_changed','source_changed','authorization_unavailable','transport_unavailable','provider_rejected','retry_exhausted','expired','recipient_suppressed')),
         ADD COLUMN provider_id uuid UNIQUE,
         ADD COLUMN delivered_at timestamptz CHECK(delivered_at IS NULL OR isfinite(delivered_at)),
         ADD COLUMN bounced_at timestamptz CHECK(bounced_at IS NULL OR isfinite(bounced_at)),
         ADD COLUMN complained_at timestamptz CHECK(complained_at IS NULL OR isfinite(complained_at)),
         ADD COLUMN hard_bounced boolean NOT NULL DEFAULT false,
         ADD CONSTRAINT product_email_feedback_status_shape CHECK((status<>'delivered' OR delivered_at IS NOT NULL) AND (status<>'bounced' OR bounced_at IS NOT NULL) AND (status<>'complained' OR complained_at IS NOT NULL)),
         ADD CONSTRAINT product_email_feedback_shape CHECK((provider_id IS NOT NULL OR (delivered_at IS NULL AND bounced_at IS NULL AND complained_at IS NULL)) AND (NOT hard_bounced OR bounced_at IS NOT NULL));
    END IF;
END;
$product_control_replay$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS product_control_email_pending_feedback (
 provider_id uuid NOT NULL,
 kind text NOT NULL CHECK(kind IN ('delivered','bounced','complained')),
 occurred_at timestamptz NOT NULL CHECK(isfinite(occurred_at)),
 hard_bounce boolean NOT NULL CHECK(NOT hard_bounce OR kind='bounced'),
 received_at timestamptz NOT NULL CHECK(isfinite(received_at)),
 PRIMARY KEY(provider_id,kind)
);
CREATE INDEX IF NOT EXISTS product_control_email_pending_feedback_expiry ON product_control_email_pending_feedback(received_at,provider_id,kind);

-- +goose Down
DROP TABLE product_control_email_pending_feedback;
UPDATE product_control_email_deliveries SET status='provider_accepted',reason='' WHERE status IN ('delivered','bounced','complained');
UPDATE product_control_email_deliveries SET reason='preferences_changed' WHERE reason='recipient_suppressed';
ALTER TABLE product_control_email_deliveries DROP CONSTRAINT product_email_feedback_shape,DROP CONSTRAINT product_email_feedback_status_shape,
 DROP COLUMN provider_id,DROP COLUMN delivered_at,DROP COLUMN bounced_at,DROP COLUMN complained_at,DROP COLUMN hard_bounced,
 DROP CONSTRAINT product_control_email_deliveries_status_check,DROP CONSTRAINT product_control_email_deliveries_reason_check,
 ADD CONSTRAINT product_control_email_deliveries_status_check CHECK(status IN ('queued','leased','captured','provider_accepted','failed','cancelled')),
 ADD CONSTRAINT product_control_email_deliveries_reason_check CHECK(reason IN ('','access_changed','preferences_changed','source_changed','authorization_unavailable','transport_unavailable','provider_rejected','retry_exhausted','expired'));
ALTER TABLE product_control_email_subscriptions DROP CONSTRAINT product_email_pause_shape,DROP COLUMN pause_reason,DROP COLUMN paused_at;
