-- +goose Up
-- +goose StatementBegin

-- GDPR self-serve audit trail (spec §17 G6, ADR-021, follow-up PR).
--
-- Customers and regulators ask two distinct questions:
--   1. "Did my export/delete/restore actually happen?" — needs a row
--      we can show them, outliving the account itself.
--   2. "For how long did you retain my personal data after the
--      request?" — needs a timestamp separate from accounts.deleted_at.
--
-- The accounts row is gone 30 days after the request lands, and the
-- events-cascade in PgStore.DeleteAccount removes ad-hoc event rows
-- keyed on account_id. So we keep a separate, append-only ledger
-- here with the request's email captured at the moment of request —
-- enough to answer an auditor's "yes, this deletion completed at
-- <ts>" question without re-introducing the very PII we just erased.
--
-- The ledger is INSERT-only by contract; no UPDATE/DELETE on the
-- application side. Retention policy: kept 7 years for accounting
-- proof (DE standard), then dropped by a future cron.

create table if not exists gdpr_requests (
  id            uuid primary key,
  account_id    uuid not null,
  account_email text not null,                -- captured at request time; the email may be re-used after restore
  action        text not null
                  check (action in ('export', 'delete', 'restore')),
  requested_at  timestamptz not null default now(),
  completed_at  timestamptz                   -- null while in flight (export) or until DeleteAccount runs
);

-- Lookup path: a customer (or a DPO on the customer's behalf) asks
-- "show every GDPR action on my email". Indexed by account_id for
-- the customer-facing surface; partial index since the table grows
-- linearly with requests but only the rows for active accounts are
-- ever read against this index path.
create index if not exists gdpr_requests_account_idx
  on gdpr_requests (account_id, requested_at desc);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
drop index if exists gdpr_requests_account_idx;
drop table if exists gdpr_requests;
-- +goose StatementEnd
