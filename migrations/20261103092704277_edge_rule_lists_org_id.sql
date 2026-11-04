-- filename: 20261103092704277_edge_rule_lists_org_id.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-091 amendment 2026-11-03 (review round 9): a list created through a
-- company app belongs to that organization. Its members manage it with the
-- app's organization permissions, and its creator's other lists stay
-- private to the creator. Lists keep their account namespace, which is the
-- one the gateway resolves a rule's lists in. NULL is a personal list.
ALTER TABLE edge_rule_lists
    ADD COLUMN IF NOT EXISTS org_id uuid REFERENCES orgs(id) ON DELETE SET NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE edge_rule_lists DROP COLUMN IF EXISTS org_id;
-- +goose StatementEnd
