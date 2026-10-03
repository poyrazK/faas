-- +goose Up
-- +goose StatementBegin
ALTER TABLE mirror_rules
    ADD COLUMN IF NOT EXISTS allow_unsafe_methods boolean NOT NULL DEFAULT false;
ALTER TABLE mirror_rules
    ALTER COLUMN percent SET DEFAULT 5;

ALTER TABLE mirror_invocation_results
    ADD COLUMN IF NOT EXISTS comparison_incomplete boolean NOT NULL DEFAULT false;

-- Pre-migration rows reused the same byte-hash predicate for schema and body
-- drift and did not record truncated snapshots, so their comparison result
-- cannot be interpreted under the new semantics.
UPDATE mirror_invocation_results
SET comparison_incomplete = true;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE mirror_invocation_results
    DROP COLUMN IF EXISTS comparison_incomplete;

ALTER TABLE mirror_rules
    DROP COLUMN IF EXISTS allow_unsafe_methods;
ALTER TABLE mirror_rules
    ALTER COLUMN percent SET DEFAULT 100;
-- +goose StatementEnd
