-- +goose Up
-- +goose StatementBegin

-- Issue #96 / ADR-025 axis 2 (slice 3): the deprecation window for
-- `snapshots.path` expires. 00022 backfilled every row's storage_key,
-- the F-1 CreateSnapshot contract refuses a row without a storage_key,
-- and #96 slice 3's proto/state cleanup removes every reader of the
-- legacy column. Drop the column; every read now flows through
-- storage_key + StorageBackend.
--
-- Idempotent (IF EXISTS) so the migration can be re-applied during
-- local development without manual cleanup.

alter table snapshots drop column if exists path;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
alter table snapshots add column if not exists path text;
-- +goose StatementEnd
