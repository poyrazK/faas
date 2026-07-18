-- +goose Up
-- +goose StatementBegin

-- G6 GDPR self-serve (spec §17 G6, ADR-021). Stamps the moment the
-- customer scheduled their account for deletion so pkg/grace can decide
-- whether the 30-day window has lapsed. NULL on every row that has
-- never been scheduled. Idempotent (IF NOT EXISTS) so the migration
-- can be re-run during local development.

alter table accounts
  add column if not exists deletion_requested_at timestamptz;

-- Partial index on the hot scan path: pkg/grace.RunOnce walks only
-- rows that are still in the deletion grace window. Partial keeps the
-- index tiny on a box with thousands of accounts that never asked for
-- deletion (the common case).
create index if not exists accounts_deletion_pending_idx
  on accounts (deletion_requested_at)
  where status = 'deleted_pending';

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
drop index if exists accounts_deletion_pending_idx;
alter table accounts drop column if exists deletion_requested_at;
-- +goose StatementEnd