-- Request analytics dimensions are normalized at the gateway edge.
-- No raw User-Agent, referrer URL, or source IP is persisted.
-- +goose Up
-- +goose StatementBegin

ALTER TABLE request_telemetry
    ADD COLUMN IF NOT EXISTS ua_family text NOT NULL DEFAULT '__unknown__'
        CHECK (ua_family IN (
            'chrome', 'edge', 'firefox', 'safari', 'opera', 'curl', 'wget',
            'python', 'go', 'java', 'bot', 'other', '__unknown__'
        )),
    ADD COLUMN IF NOT EXISTS referrer_host text NOT NULL DEFAULT '__none__'
        CHECK (
            length(referrer_host) BETWEEN 1 AND 253
            AND referrer_host = lower(referrer_host)
            AND referrer_host !~ '[/?#[:space:]]'
        ),
    ADD COLUMN IF NOT EXISTS country text NOT NULL DEFAULT '__unknown__'
        CHECK (country = '__unknown__' OR country ~ '^[A-Z]{2}$');

-- The analytics queries already use the app/time index. These indexes are
-- intentionally not dimension indexes: dimensions are bounded in the row and
-- grouped only within a customer's retention window, avoiding label-style
-- index/cardinality growth.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
