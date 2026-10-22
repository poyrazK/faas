-- filename: 20261022085947790_edge_crawler_ip_ranges.sql

-- ADR-20261022083601: crawler operators' published IP ranges. apid refreshes
-- one row per crawler; gateways read them to verify AI crawlers
-- (verified_ai_crawler) and, without a DNS lookup, search crawlers
-- (verified_bot). Platform data: no account, no app.
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS edge_crawler_ip_ranges (
    crawler text PRIMARY KEY
        CONSTRAINT edge_crawler_ip_ranges_crawler_check CHECK (crawler ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
    source_url text NOT NULL
        CONSTRAINT edge_crawler_ip_ranges_source_url_check CHECK (source_url LIKE 'https://%' AND length(source_url) <= 512),
    prefixes text[] NOT NULL
        CONSTRAINT edge_crawler_ip_ranges_prefixes_check CHECK (cardinality(prefixes) BETWEEN 1 AND 5000),
    source_created_at text
        CONSTRAINT edge_crawler_ip_ranges_source_created_at_check CHECK (length(source_created_at) <= 64),
    fetched_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS edge_crawler_ip_ranges;
-- +goose StatementEnd
