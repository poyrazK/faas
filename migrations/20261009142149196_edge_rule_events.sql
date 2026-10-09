-- filename: 20261009142149196_edge_rule_events.sql
--
-- ADR-834: sampled edge-rule security events. Gateways keep up to 10 full
-- events per rule per minute and write them in one batch with the ADR-830
-- hit counts; rows are pruned after 7 days. No query strings, bodies or
-- headers other than the user agent are stored.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS edge_rule_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    rule_id     uuid NOT NULL,
    app_id      uuid NOT NULL,
    occurred_at timestamptz NOT NULL,
    outcome     text NOT NULL CHECK (outcome IN ('matched', 'logged')),
    request_id  text NOT NULL DEFAULT '' CHECK (length(request_id) <= 128),
    method      text NOT NULL DEFAULT '' CHECK (length(method) <= 16),
    host        text NOT NULL DEFAULT '' CHECK (length(host) <= 253),
    path        text NOT NULL DEFAULT '' CHECK (octet_length(path) <= 1024),
    client_ip   inet,
    country     text NOT NULL DEFAULT '' CHECK (length(country) <= 8),
    user_agent  text NOT NULL DEFAULT '' CHECK (octet_length(user_agent) <= 256)
);

CREATE INDEX IF NOT EXISTS edge_rule_events_app_time_idx
    ON edge_rule_events (app_id, occurred_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS edge_rule_events_rule_time_idx
    ON edge_rule_events (rule_id, occurred_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS edge_rule_events_occurred_idx
    ON edge_rule_events (occurred_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS edge_rule_events;
-- +goose StatementEnd
