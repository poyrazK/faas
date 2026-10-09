-- filename: 20261009121752609_app_webhooks_datadog_delivery_format.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-742: allow the datadog delivery format. apid and the webhook dispatcher
-- restrict its target to the supported Datadog Events API endpoints.
ALTER TABLE app_webhooks DROP CONSTRAINT IF EXISTS app_webhooks_delivery_format_chk;
ALTER TABLE app_webhooks ADD CONSTRAINT app_webhooks_delivery_format_chk
  CHECK (delivery_format = ANY (ARRAY['json'::text, 'cloudevents'::text, 'datadog'::text]));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The narrower check cannot hold datadog rows, and their secret is a Datadog
-- API key rather than a signing secret, so rolling back removes them.
DELETE FROM app_webhooks WHERE delivery_format = 'datadog';
ALTER TABLE app_webhooks DROP CONSTRAINT IF EXISTS app_webhooks_delivery_format_chk;
ALTER TABLE app_webhooks ADD CONSTRAINT app_webhooks_delivery_format_chk
  CHECK (delivery_format = ANY (ARRAY['json'::text, 'cloudevents'::text]));
-- +goose StatementEnd
