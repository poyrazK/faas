-- filename: 00570_instances_mode_widen.sql
-- +goose Up
-- +goose StatementBegin

-- issue #1186 / ADR-137 §Decision 1 + ADR-125 follow-on — widens the
-- `instances.mode` CHECK from the ADR-125 two-value closed set
-- {normal, mirror} to the M-2 five-value closed set
-- {normal, mirror, worker, service, job}.
--
-- Why DROP + ADD rather than ALTER ... ADD VALUE:
--   * ALTER TABLE ... ALTER CONSTRAINT is not supported by Postgres
--     for CHECK constraints (only for FK). The standard idiom is
--     DROP + ADD in a single transaction so writers see one atomic
--     shape, not a "no constraint" window.
--   * DROP CONSTRAINT IF EXISTS + ADD CONSTRAINT IF NOT EXISTS makes
--     the migration idempotent against a partially-applied state
--     (a re-run on a half-applied DB is a no-op). The goose Apply
--     driver is single-shot, but pgtest re-runs the migration set
--     against a fresh schema per test so the idempotence is real.
--
-- Why this widening is safe:
--   * The closed set is a strict superset; pre-M-2 rows
--     (mode='normal' or mode='mirror') are valid in both shapes.
--   * The partial index `instances_mode_idx` on (app_id, mode) WHERE
--     mode = 'mirror' is preserved verbatim (migrations/00385) — the
--     predicate still matches every row that matches today, and the
--     new mode values cannot match the predicate.
--   * The DROP INDEX / re-CREATE INDEX is NOT needed because the
--     index predicate (mode = 'mirror') is unchanged.
--
-- Constraint name:
--   * The original migration 00385 declared the CHECK inline in the
--     column definition; Postgres auto-names the resulting
--     constraint `instances_mode_check` (the canonical pattern
--     `<table>_<column>_check` for inline column CHECKs). DROP
--     CONSTRAINT IF EXISTS uses that name; the IF EXISTS form
--     survives any rename that a future migration may apply.

ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_mode_check;

ALTER TABLE instances
    ADD CONSTRAINT instances_mode_check
    CHECK (mode IN ('normal', 'mirror', 'worker', 'service', 'job'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Reverse the widening by restoring the ADR-125 two-value shape.
-- Any rows stamped with the new M-2 mode values (worker/service/job)
-- must be transitioned back to 'normal' BEFORE the constraint is
-- tightened; otherwise the DROP+ADD round-trip below would fail
-- with SQLSTATE 23514 (check_violation). The CASE expression maps
-- every non-legacy value to 'normal' which is the safe downgrade
-- (the new-mode semantics — long-running daemon, replicated service,
-- run-to-completion — are lost on downgrade, which is acceptable
-- because going down means reverting M-2 entirely).
UPDATE instances
   SET mode = 'normal'
 WHERE mode NOT IN ('normal', 'mirror');

ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_mode_check;

ALTER TABLE instances
    ADD CONSTRAINT instances_mode_check
    CHECK (mode IN ('normal', 'mirror'));

-- +goose StatementEnd