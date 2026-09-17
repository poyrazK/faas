-- +goose Up
-- +goose StatementBegin

-- Customer audit drill-downs filter event payloads by the owning app. Keep
-- each relationship key indexed so a missing app can be proven without
-- scanning the account's complete event history (issue #2688).
CREATE INDEX IF NOT EXISTS events_data_app_id_idx
    ON events ((data->>'app_id'))
    WHERE data ? 'app_id';

CREATE INDEX IF NOT EXISTS events_data_deployment_id_idx
    ON events ((data->>'deployment_id'))
    WHERE data ? 'deployment_id';

CREATE INDEX IF NOT EXISTS events_data_build_id_idx
    ON events ((data->>'build_id'))
    WHERE data ? 'build_id';

CREATE INDEX IF NOT EXISTS events_data_instance_id_idx
    ON events ((data->>'instance_id'))
    WHERE data ? 'instance_id';

CREATE INDEX IF NOT EXISTS events_data_instance_idx
    ON events ((data->>'instance'))
    WHERE data ? 'instance';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS events_data_instance_id_idx;
DROP INDEX IF EXISTS events_data_instance_idx;
DROP INDEX IF EXISTS events_data_build_id_idx;
DROP INDEX IF EXISTS events_data_deployment_id_idx;
DROP INDEX IF EXISTS events_data_app_id_idx;
-- +goose StatementEnd
