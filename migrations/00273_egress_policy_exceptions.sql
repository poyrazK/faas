-- filename: 00273_egress_policy_exceptions.sql
-- +goose Up
-- +goose StatementBegin
--
-- 00273_egress_policy_exceptions.sql — issue #911 / ADR-110 /
-- PR scale-out tier-1 residual (Gap #4).
--
-- Extends the egress_policy singleton (00078_egress_policy_notify.sql)
-- with two new columns: the deny-set exception list + the
-- lateral-movement gate. The CHECK constraint pairs them: the
-- gate cannot be set without at least one exception. This is
-- the row-level mirror of the manifest validator's same pair
-- (egress.danger_accept_rfc1918_lateral_movement +
-- egress.overlay_exceptions in pkg/manifest/manifest.go).
--
-- The pair-as-constraint design is load-bearing safety: an
-- operator who flips the manifest flag without listing an
-- exception gets a clean startup-time validator error
-- (manifest-load) AND a clean DB CHECK violation (apply-time).
-- The two paths converge on the same pair — no silent bypass.
--
-- Replay-safety (the contract migrations/replay_safety_test.go
-- asserts): every DDL is IF NOT EXISTS / DO-block-guarded. A
-- drifted box (schema present, goose row missing) re-applies
-- cleanly without tripping SQLSTATE 42P07 / 42710.

-- overlay_exceptions text[] — list of CIDR strings the renderer
-- emits as accept rules BEFORE the §11 deny block on the host
-- forward chain + per-netns forward chain. Empty by default
-- (single-host dev keeps the legacy "deny wins on RFC1918"
-- posture byte-identical).
alter table egress_policy
    add column if not exists overlay_exceptions text[] not null default '{}';

-- danger_accept_rfc1918_lateral_movement boolean — gate that
-- enables the deny-set exception path. Default false. Operators
-- using an RFC1918 overlay MUST set this AND list the overlay
-- CIDR in overlay_exceptions; the CHECK constraint below
-- enforces the pair at the row level.
alter table egress_policy
    add column if not exists danger_accept_rfc1918_lateral_movement boolean not null default false;

-- Pair-enforcing CHECK constraint. The constraint name is
-- generated from the table + column list to be deterministic
-- across replay; the DO-block guard prevents SQLSTATE 42710
-- (duplicate object) on a re-apply after the schema is in place
-- but the goose row is missing (the drift case the replay-safety
-- test guards).
do $$
begin
    if not exists (
        select 1
          from pg_constraint
         where conname = 'egress_policy_pair_check'
           and conrelid = 'egress_policy'::regclass
    ) then
        alter table egress_policy
            add constraint egress_policy_pair_check
            check (
                not danger_accept_rfc1918_lateral_movement
                or coalesce(array_length(overlay_exceptions, 1), 0) > 0
            );
    end if;
end$$;

-- Replace the trigger function so the JSON payload includes the
-- two new fields (the watcher's egressPolicyAuditRow struct reads
-- them for log correlation). This is the canonical place for the
-- payload expansion — 00078_egress_policy_notify.sql stays as it
-- is so a 00078-only replay against a pre-00273 schema doesn't
-- fail on `new.overlay_exceptions` (the column doesn't exist
-- yet). After 00273 applies, the trigger function is replaced
-- once and all subsequent updates emit the full 6-field payload.
create or replace function egress_policy_notify() returns trigger as $$
begin
    perform pg_notify(
        'egress_policy_changed',
        json_build_object(
            'policy_id', new.id,
            'public_iface', new.public_iface,
            'masquerade_cidr', new.masquerade_cidr,
            'overlay_exceptions', new.overlay_exceptions,
            'danger_accept_rfc1918_lateral_movement', new.danger_accept_rfc1918_lateral_movement,
            'changed_at', new.changed_at
        )::text
    );
    return null;
end;
$$ language plpgsql;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

alter table if exists egress_policy drop constraint if exists egress_policy_pair_check;
alter table if exists egress_policy drop column if exists danger_accept_rfc1918_lateral_movement;
alter table if exists egress_policy drop column if exists overlay_exceptions;

-- +goose StatementEnd