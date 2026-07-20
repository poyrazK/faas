-- +goose Up
-- +goose StatementBegin

-- Finding #1 (spec §4.7): one paid-tier "≥100 %" quota_warning per
-- UTC day. pkg/meter/quota.go::EnforceQuota previously emitted the
-- pg_notify event on every quota tick (60 s), producing ~1,440 frames
-- per day for an account that stayed over quota. The in-process
-- dedupe promised by the comment was lost on every daemon restart.
-- This column is the persistent anchor — stamped atomically with the
-- notify emission. NULL on every row that has never tripped.
--
-- Idempotent (IF NOT EXISTS) so the migration can be re-run during
-- local development.

alter table accounts
  add column if not exists last_quota_warning_at timestamptz;

-- Finding #2 (spec §4.7, §17 dunning): anchor for the 7-day
-- past_due → suspended and the 21-day suspended → deleted_pending
-- transitions that pkg/meter.Dunning drives. Stamped by the
-- apid webhook on invoice.payment_failed (apid/handlers_ext.go),
-- backfilled if missing, advanced by the dunning timer.
--
-- NULL on accounts that have never been past_due. Idempotent.

alter table accounts
  add column if not exists past_due_at timestamptz;

-- Partial index: pkg/meter.Dunning.RunOnce walks only rows in the
-- past_due state. Mirrors accounts_deletion_pending_idx from
-- migration 00010 — same scan shape, same justification (partial
-- keeps the index tiny on a box with thousands of never-past-due
-- accounts).

create index if not exists accounts_past_due_idx
  on accounts (past_due_at)
  where status = 'past_due';

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
drop index if exists accounts_past_due_idx;
alter table accounts drop column if exists past_due_at;
alter table accounts drop column if exists last_quota_warning_at;
-- +goose StatementEnd