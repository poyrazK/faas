-- +goose Up
-- +goose StatementBegin
--
-- 00048_invoices.sql — issue #259 (BILLING: plan comparison + invoice history).
--
-- Slot note: slot 00047 was claimed on main by
-- 00047_crons_created_at.sql (retroactive crons.created_at fix) before
-- this PR was rebased. Renumbered to 00048 to keep the append-only
-- migration sequence contiguous. See memory
-- migration-slot-collisions-across-prs.md.
--
-- Persists billing-provider invoices so the new GET /v1/invoices surface
-- and /dashboard/invoices page can list a customer's billing history
-- without redirecting to the Stripe/Paddle portal.
--
-- Idempotency: UNIQUE (account_id, provider, provider_invoice_id) is the
-- webhook-replay guard. Two concurrent retries for the same invoice
-- collide on insert; the loser's row is dropped via ON CONFLICT DO
-- NOTHING. No separate dedupe table is needed — the constraint IS the
-- dedupe row. Mirrors the per-account dedupe tables in migrations 00041
-- (paddle_overage_claim_window) and 00004 (stripe_push_dedupe).
--
-- GDPR: ON DELETE CASCADE on account_id so DeleteAccount
-- (pkg/state/pgstore.go) cleanly removes invoice rows in the same
-- transaction that scrubs the rest of the customer's data. raw (jsonb)
-- carries the customer's billing email as the provider delivered it —
-- already public-web-facing from the provider's side, but worth
-- confirming before a hard-delete against a production customer.
--
-- Money: integer cents (provider native). The financial model
-- distills to EUR cents at the API edge; never float on money.
--
-- PDF: pdf_available is what we expose to the API. The hosted PDF URL
-- is a provider session-scoped link — we never expose it. Spec
-- acceptance #4 says "whether a PDF is available", not "the PDF URL".
--
-- Index: (account_id, period_end DESC, id DESC) supports the dashboard
-- "most-recent-first" listing and the deterministic cursor pagination
-- used by GET /v1/invoices.

create table invoices (
  id                  uuid primary key default gen_random_uuid(),
  account_id          uuid not null references accounts(id) on delete cascade,
  provider            text not null check (provider in ('stripe','paddle','polar')),
  provider_invoice_id text not null,
  number              text not null default '',
  status              text not null check (status in ('draft','open','paid','uncollectible','void')),
  period_start        timestamptz not null,
  period_end          timestamptz not null,
  subtotal_cents      bigint not null default 0 check (subtotal_cents >= 0),
  tax_cents           bigint not null default 0 check (tax_cents >= 0),
  total_cents         bigint not null default 0 check (total_cents >= 0),
  amount_paid_cents   bigint not null default 0 check (amount_paid_cents >= 0),
  currency            text not null default 'eur' check (currency = 'eur'),
  pdf_available       boolean not null default false,
  hosted_url          text not null default '',
  raw                 jsonb not null default '{}'::jsonb,
  created_at          timestamptz not null default now(),
  updated_at          timestamptz not null default now(),
  unique (account_id, provider, provider_invoice_id)
);

create index invoices_account_period_idx
  on invoices (account_id, period_end desc, id desc);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table if exists invoices;
-- +goose StatementEnd
