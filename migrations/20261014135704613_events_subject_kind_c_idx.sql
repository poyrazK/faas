-- filename: 20261014135704613_events_subject_kind_c_idx.sql
--
-- H10-43: GET /v1/audit-events?kind_prefix= answered 503 "could not list
-- audit events" on production-us. ListCustomerEvents filtered kinds with
-- left(kind, n) = prefix, which no index can serve, so a selective prefix
-- walked events_subject_at_id_idx through every event the account owns
-- (73k rows, mostly cron.fired and wake.admitted; ~750 ms warm) and hit the
-- request deadline cold. The query now adds a byte-order range on kind that
-- this index serves; a selective prefix reads only its matches (0.2 ms on a
-- copy of the same rows).
--
-- COLLATE "C" pins the range to byte order, so it holds every kind that
-- starts with the prefix whatever the database collation is.
--
-- Not CONCURRENTLY: pkg/db.MigrateUp runs every file in a transaction. The
-- build briefly blocks event inserts (about a second for production-us's
-- 394k rows), the same trade 00073_snapshots_live_idx.sql made.

-- +goose Up
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS events_subject_kind_c_at_id_idx
    ON events (subject, kind COLLATE "C", at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.events_subject_kind_c_at_id_idx;
-- +goose StatementEnd
