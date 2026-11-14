-- migration-compat: expand
-- filename: 20261112120000312_managed_postgres_plan_excess_reminder.sql
--
-- ADR-20261110184700 amendment 2026-11-12: a database past its plan is
-- deleted on the date its suspension email named, and its owner is reminded
-- once, a week before that date. The mail outbox links the reminder's intent
-- here in the same transaction that queues it, so a deletion date is reminded
-- at most once. The reminder reuses the managed_postgres_plan_excess template,
-- so no template CHECK changes. The previous release never names the column.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_plan_excess_notices
    ADD COLUMN IF NOT EXISTS reminder_mail_intent_id uuid;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_plan_excess_notices
    DROP COLUMN IF EXISTS reminder_mail_intent_id;
-- +goose StatementEnd
