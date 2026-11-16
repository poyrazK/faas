-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Control-plane API problems returned to an authenticated customer
-- (ADR-20261112170000). Route is the matched pattern, never the raw URL;
-- no request or response body is stored. apid prunes rows after 14 days.
CREATE TABLE IF NOT EXISTS customer_api_errors (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 occurred_at timestamptz NOT NULL CHECK(isfinite(occurred_at)),
 route text NOT NULL CHECK(octet_length(route) BETWEEN 1 AND 200),
 code text NOT NULL CHECK(octet_length(code) BETWEEN 1 AND 80),
 app_slug text NOT NULL DEFAULT '' CHECK(octet_length(app_slug)<=63),
 request_id text NOT NULL DEFAULT '' CHECK(octet_length(request_id)<=128),
 trace_id text NOT NULL DEFAULT '' CHECK(trace_id='' OR trace_id ~ '^[0-9a-f]{32}$')
);
CREATE INDEX IF NOT EXISTS customer_api_errors_account_idx ON customer_api_errors(account_id,occurred_at DESC);
CREATE INDEX IF NOT EXISTS customer_api_errors_request_idx ON customer_api_errors(request_id) WHERE request_id<>'';
CREATE INDEX IF NOT EXISTS customer_api_errors_trace_idx ON customer_api_errors(trace_id) WHERE trace_id<>'';
CREATE INDEX IF NOT EXISTS customer_api_errors_age_idx ON customer_api_errors(occurred_at);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS customer_api_errors;
