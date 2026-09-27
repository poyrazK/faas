-- filename: 20260927190000000_request_id_journal.sql
-- +goose Up
-- +goose StatementBegin
--
-- Keep an exact, durable mapping from the public x-faas-request-id to its
-- app, account, arrival time, and W3C trace id. This is deliberately separate
-- from request_telemetry: that table is sampled/collapsed and may be disabled
-- or rate-limited, so it cannot guarantee lookup by every public request ID.
-- No path, payload, headers, or response data is retained here.
CREATE TABLE IF NOT EXISTS request_id_journal (
    id          uuid        PRIMARY KEY,
    account_id  uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id      uuid        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    request_id  text        NOT NULL,
    trace_id    text,
    received_at timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    CONSTRAINT request_id_journal_request_id_size_chk
        CHECK (octet_length(request_id) BETWEEN 1 AND 128),
    CONSTRAINT request_id_journal_request_id_control_chk
        CHECK (request_id !~ '[[:cntrl:]]'),
    CONSTRAINT request_id_journal_trace_id_format_chk
        CHECK (trace_id IS NULL OR trace_id ~ '^[0-9a-f]{32}$'),
    CONSTRAINT request_id_journal_expiry_chk
        CHECK (expires_at > received_at)
);

CREATE INDEX IF NOT EXISTS request_id_journal_app_request_received_idx
    ON request_id_journal (app_id, request_id, received_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS request_id_journal_expires_idx
    ON request_id_journal (expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS request_id_journal;
-- +goose StatementEnd
