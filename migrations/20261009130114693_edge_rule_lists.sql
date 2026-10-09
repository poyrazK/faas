-- filename: 20261009130114693_edge_rule_lists.sql
--
-- ADR-907: reusable edge-rule lists. Account-scoped, named, typed sets of
-- IPs/CIDRs, countries, hosts or strings that match conditions reference
-- with {"op": "in_list", "list": "<name>"}. apid writes items already
-- validated and canonicalized; a list write touches the referencing rules'
-- updated_at so the edge_rules change log invalidates gateway caches.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS edge_rule_lists (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id  uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name        text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9_-]{0,63}$'),
    kind        text NOT NULL CHECK (kind IN ('ip', 'country', 'host', 'string')),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 500),
    items       text[] NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT edge_rule_lists_account_name_key UNIQUE (account_id, name)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS edge_rule_lists;
-- +goose StatementEnd
