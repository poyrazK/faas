-- +goose Up
-- +goose StatementBegin

ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_state_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_state_chk
    CHECK (state IN ('ok', 'firing', 'degraded'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

UPDATE alert_rules SET state = 'ok' WHERE state = 'degraded';
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_state_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_state_chk
    CHECK (state IN ('ok', 'firing'));

-- +goose StatementEnd
