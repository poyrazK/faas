-- filename: 20261010085843066_edge_rule_lists_asn_kind.sql
--
-- ADR-966: edge-rule lists gain the 'asn' kind (autonomous system numbers,
-- stored as canonical decimal strings) for the asn match field.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE edge_rule_lists DROP CONSTRAINT IF EXISTS edge_rule_lists_kind_check;
ALTER TABLE edge_rule_lists
    ADD CONSTRAINT edge_rule_lists_kind_check CHECK (kind IN ('ip', 'country', 'host', 'string', 'asn'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM edge_rule_lists WHERE kind = 'asn';
ALTER TABLE edge_rule_lists DROP CONSTRAINT IF EXISTS edge_rule_lists_kind_check;
ALTER TABLE edge_rule_lists
    ADD CONSTRAINT edge_rule_lists_kind_check CHECK (kind IN ('ip', 'country', 'host', 'string'));
-- +goose StatementEnd
