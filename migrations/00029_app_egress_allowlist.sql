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

alter table apps
  add column if not exists egress_allowlist cidr[] not null default '{}';

-- v4-only v1. Postgres CHECK constraints cannot reference aggregate
-- functions (bool_and/family(cidr) is not allowed) — that approach
-- compiles into a SQL parse error: `column "cidr" does not exist`.
-- A CHECK on the OUTER array with a helper function marked IMMUTABLE
-- could also work, but a BEFORE-row TRIGGER is more idiomatic for
-- "validate per-element of an array column" and keeps the constraint
-- logic visible in DDL rather than off in a plpgsql function. The
-- trigger fires on INSERT and UPDATE; empty arrays short-circuit
-- (the documented default-"no allowlist" state). The guard mirrors
-- the apid PATCH handler's parse step — apid catches the operator-
-- friendly error first, the trigger is the floor for any code path
-- that bypasses apid (vmmd wire side, sqlc-flavoured tooling,
-- manual psql).
--
-- IF NOT EXISTS pattern: drop + create, both idempotent, mirrors the
-- ALTER … DROP CONSTRAINT pattern the rest of the migrations use.
drop trigger if exists apps_egress_allowlist_v4_only on apps;
create or replace function apps_egress_allowlist_v4_only_check()
  returns trigger
  language plpgsql
  as $$
declare
  bad cidr;
begin
  if new.egress_allowlist is null or cardinality(new.egress_allowlist) = 0 then
    return new;
  end if;
  for bad in
    select c
      from unnest(new.egress_allowlist) c
     where family(c) <> 4
    limit 1
  loop
    raise exception 'apps_egress_allowlist: v4-only v1 (got %)', bad
      using errcode = '23514',
            constraint = 'apps_egress_allowlist_v4_only';
  end loop;
  return new;
end;
$$;
create trigger apps_egress_allowlist_v4_only
  before insert or update of egress_allowlist on apps
  for each row
  execute function apps_egress_allowlist_v4_only_check();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

drop trigger if exists apps_egress_allowlist_v4_only on apps;
drop function if exists apps_egress_allowlist_v4_only_check();
alter table apps drop column if exists egress_allowlist;

-- +goose StatementEnd
