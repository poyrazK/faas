-- +goose Up
-- +goose StatementBegin

-- ADR-031 (M8 tier-2 network roadmap): per-app outbound IP allowlist.
-- Default empty (-> no rule emitted, current behaviour preserved).
-- Non-empty -> the per-netns forward chain gains a single
--   `iifname tap0 ip daddr { <egress_allowlist> } accept`
-- rule AFTER the lateral-movement deny and SMTP drops. The deny
-- ALWAYS wins on overlap (deny > allow), so an operator typo'ing
-- `10.0.0.0/8` into an allowlist still gets dropped by the
-- lateral-movement deny. v4-only v1; v6 mirror deferred (matches
-- the ADR-023 v4/v6 split).
--
-- Schema choice: cidr[] column on apps, not a child table. The read
-- pattern is "all CIDRs for one app on wake" — pk lookup only, no
-- join needed. The write pattern is "atomic full-replace" — one
-- row UPDATE; a child table needs DELETE + N INSERTs inside a tx.
-- The per-plan cap is a count of *entries*, validated in the API
-- layer (pkg/api/limits.go EgressAllowlistMaxSize) before SQL.
-- Migration 00007 (compute_node bind fields) made the same call.
--
-- CHECK constraint: cidr[] does not carry a per-element CHECK in
-- Postgres. The family check is a function constraint over the
-- array. Empty array stays allowed (the default — "no allowlist").

alter table apps
  add column if not exists egress_allowlist cidr[] not null default '{}';

-- The constraint is not guarded by IF NOT EXISTS (Postgres doesn't
-- support that on ADD CONSTRAINT). The drop + add pair matches the
-- 00010 / 00011 pattern, so the migration is idempotent across
-- re-runs during local development.
alter table apps drop constraint if exists apps_egress_allowlist_v4_only;
alter table apps
  add constraint apps_egress_allowlist_v4_only
    check (
      cardinality(egress_allowlist) = 0
      or bool_and(family(cidr) = 4)
    );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

alter table apps drop constraint if exists apps_egress_allowlist_v4_only;
alter table apps drop column if exists egress_allowlist;

-- +goose StatementEnd
