-- filename: 20261009121926438_api_consumer_usage_statement_revisions.sql

-- ADR-843: app-local API consumer usage statements adopt the platform-tenant
-- revision model. A changed draft is superseded by a new revision, and usage
-- that arrives after finalization becomes an additive adjustment revision of
-- the same period instead of being unreachable. Existing rows become
-- revision 1 with their current status.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE api_consumer_usage_statements
  ADD COLUMN IF NOT EXISTS revision integer NOT NULL DEFAULT 1;

ALTER TABLE api_consumer_usage_statements
  DROP CONSTRAINT IF EXISTS api_consumer_usage_statements_revision_chk;
ALTER TABLE api_consumer_usage_statements
  ADD CONSTRAINT api_consumer_usage_statements_revision_chk CHECK (revision > 0);

ALTER TABLE api_consumer_usage_statements
  DROP CONSTRAINT IF EXISTS api_consumer_usage_statements_status_chk;
ALTER TABLE api_consumer_usage_statements
  ADD CONSTRAINT api_consumer_usage_statements_status_chk
    CHECK (status IN ('draft', 'finalized', 'superseded'));

ALTER TABLE api_consumer_usage_statements
  DROP CONSTRAINT IF EXISTS api_consumer_usage_statements_finalized_chk;
ALTER TABLE api_consumer_usage_statements
  ADD CONSTRAINT api_consumer_usage_statements_finalized_chk
    CHECK ((status IN ('draft', 'superseded') AND finalized_at IS NULL)
        OR (status = 'finalized' AND finalized_at IS NOT NULL));

ALTER TABLE api_consumer_usage_statements
  DROP CONSTRAINT IF EXISTS api_consumer_usage_statements_period_uniq;
ALTER TABLE api_consumer_usage_statements
  DROP CONSTRAINT IF EXISTS api_consumer_usage_statements_period_revision_uniq;
ALTER TABLE api_consumer_usage_statements
  ADD CONSTRAINT api_consumer_usage_statements_period_revision_uniq
    UNIQUE (app_id, consumer_id, period_start, period_end, revision);

COMMENT ON COLUMN api_consumer_usage_statements.revision IS
  'Monotonic revision within one exact period. Revisions after a finalized one carry only later usage.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Later revisions cannot fit the single-statement-per-period shape; their
-- handoffs cascade with them.
DELETE FROM api_consumer_usage_statements WHERE revision > 1;
ALTER TABLE api_consumer_usage_statements
  DROP CONSTRAINT IF EXISTS api_consumer_usage_statements_period_revision_uniq;
ALTER TABLE api_consumer_usage_statements
  ADD CONSTRAINT api_consumer_usage_statements_period_uniq
    UNIQUE (app_id, consumer_id, period_start, period_end);
UPDATE api_consumer_usage_statements SET status = 'draft' WHERE status = 'superseded';
ALTER TABLE api_consumer_usage_statements
  DROP CONSTRAINT IF EXISTS api_consumer_usage_statements_finalized_chk;
ALTER TABLE api_consumer_usage_statements
  ADD CONSTRAINT api_consumer_usage_statements_finalized_chk
    CHECK ((status = 'draft' AND finalized_at IS NULL)
        OR (status = 'finalized' AND finalized_at IS NOT NULL));
ALTER TABLE api_consumer_usage_statements
  DROP CONSTRAINT IF EXISTS api_consumer_usage_statements_status_chk;
ALTER TABLE api_consumer_usage_statements
  ADD CONSTRAINT api_consumer_usage_statements_status_chk
    CHECK (status IN ('draft', 'finalized'));
ALTER TABLE api_consumer_usage_statements
  DROP CONSTRAINT IF EXISTS api_consumer_usage_statements_revision_chk;
ALTER TABLE api_consumer_usage_statements
  DROP COLUMN IF EXISTS revision;
-- +goose StatementEnd
