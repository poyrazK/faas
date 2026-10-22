-- filename: 20261013081608417_outbound_ai_request_content.sql
-- ADR-996: opt-in capture of AI call content. With the request log on, an
-- integration may also keep the first bytes of each logged call's request
-- and response bodies, never headers, for a few days and a capped number of
-- calls per UTC day. outboundd writes and prunes the rows; apid reads them.
-- Additive and replay-safe.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS ai_request_log_content boolean NOT NULL DEFAULT false;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_integrations_ai_request_log_content_chk' AND conrelid = 'outbound_integrations'::regclass) THEN
        ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_ai_request_log_content_chk
            CHECK (NOT ai_request_log_content OR ai_request_log_enabled);
    END IF;
END$$;

CREATE TABLE IF NOT EXISTS outbound_ai_request_content (
    log_id                uuid PRIMARY KEY REFERENCES outbound_ai_request_log (id) ON DELETE CASCADE,
    integration_id        uuid NOT NULL REFERENCES outbound_integrations (id) ON DELETE CASCADE,
    created_at            timestamptz NOT NULL DEFAULT now(),
    request_body          bytea NOT NULL,
    request_truncated     boolean NOT NULL,
    response_body         bytea NOT NULL,
    response_truncated    boolean NOT NULL,
    response_content_type text,
    CONSTRAINT outbound_ai_request_content_size_chk
        CHECK (octet_length(request_body) <= 32768 AND octet_length(response_body) <= 32768),
    CONSTRAINT outbound_ai_request_content_type_chk
        CHECK (response_content_type IS NULL
               OR (length(response_content_type) BETWEEN 1 AND 128 AND response_content_type ~ '^[ -~]+$'))
);
CREATE INDEX IF NOT EXISTS outbound_ai_request_content_daily_idx
    ON outbound_ai_request_content (integration_id, created_at);
CREATE INDEX IF NOT EXISTS outbound_ai_request_content_prune_idx
    ON outbound_ai_request_content (created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS outbound_ai_request_content;
ALTER TABLE outbound_integrations
    DROP CONSTRAINT IF EXISTS outbound_integrations_ai_request_log_content_chk,
    DROP COLUMN IF EXISTS ai_request_log_content;
-- +goose StatementEnd
