-- +goose Up
-- ADR-749 slice 2: which notification channels an alert rule delivers to.
-- Deleting either side removes the binding. apid is the only writer.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS alert_rule_channels (
    rule_id uuid NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    channel_id uuid NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    CONSTRAINT alert_rule_channels_pkey PRIMARY KEY (rule_id, channel_id)
);
CREATE INDEX IF NOT EXISTS alert_rule_channels_channel_idx ON alert_rule_channels (channel_id);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS alert_rule_channels;
