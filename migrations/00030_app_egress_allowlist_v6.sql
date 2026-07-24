-- +goose Up
-- +goose StatementBegin

-- ADR-032: per-app egress allowlist v6 mirror. The v4-only
-- trigger shipped with 00029 is replaced by a v4-or-v6, non-/0
-- guard. Existing rows are 100% v4 by construction (the v4-only
-- trigger rejected every v6 write since 00029 landed), so this is
-- a contract swap, not a data migration. Wire shape (single
-- repeated string at proto AppSpec field 7) and
-- Config.EgressAllowlist (single []netip.Prefix) stay single; the
-- renderer partitions by prefix.Addr().Is4() and emits two nft
-- rules — one ip, one ip6 — using the existing table-family split
-- (ADR-023). Plan caps (Pro 16, Scale 64) stay shared by v4 + v6
-- combined; per-family caps are deferred.

drop trigger if exists apps_egress_allowlist_v4_only on apps;
drop function if exists apps_egress_allowlist_v4_only_check();

create or replace function apps_egress_allowlist_cidr_check()
  returns trigger
  language plpgsql
  as $$
declare
  bad cidr;
begin
  if new.egress_allowlist is null or cardinality(new.egress_allowlist) = 0 then
    return new;
  end if;
  -- Per-entry guards: family must be v4 or v6, mask must be non-zero.
  -- The /0 reject closes the same hole as the v4-only trigger's
  -- `prefix.Bits() == 0` reject at the wire + apid layers: an
  -- operator cannot pin "the entire address space" — that is the
  -- chain-policy accept's job, not the allowlist's. Two narrow
  -- selects (one per guard) keep the error messages specific; a
  -- combined select with bool_or would conflate family and masklen
  -- failures and force a parser to guess.
  for bad in
    select c
      from unnest(new.egress_allowlist) c
     where family(c) not in (4, 6)
     limit 1
  loop
    raise exception 'apps_egress_allowlist: only v4 or v6 CIDRs (got family % for %)', family(bad), bad
      using errcode = '23514',
            constraint = 'apps_egress_allowlist_cidr';
  end loop;
  for bad in
    select c
      from unnest(new.egress_allowlist) c
     where masklen(c) = 0
     limit 1
  loop
    raise exception 'apps_egress_allowlist: rejected % (masklen /0; ADR-032 non-/0 contract)', bad
      using errcode = '23514',
            constraint = 'apps_egress_allowlist_cidr';
  end loop;
  return new;
end;
$$;

create trigger apps_egress_allowlist_cidr
  before insert or update of egress_allowlist on apps
  for each row
  execute function apps_egress_allowlist_cidr_check();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Down-migrate restores the v4-only trigger so 00029's RejectsV6
-- (which becomes AcceptsV6 only after 00030 ships; the original
-- v4-only contract is what 00029 originally pinned) passes on a
-- clean reverse. Same shape as 00029's up-migrate, verbatim.

drop trigger if exists apps_egress_allowlist_cidr on apps;
drop function if exists apps_egress_allowlist_cidr_check();

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