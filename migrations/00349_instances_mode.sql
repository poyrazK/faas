-- filename: 00349_instances_mode.sql
-- +goose Up
-- +goose StatementBegin
--
-- issue #72 / ADR-125 — traffic mirroring. Adds the `mode` column
-- to instances so the schedd can stamp "this VM was woken for a
-- mirror call" at creation time, and so pkg/meter/sampler.go can
-- skip mirror instances at billing time.
--
-- Why a column and not a separate table:
--   * mode is an attribute of the instance (like state, like
--     deployment_id), not a separate concept with its own
--     lifecycle. A join-free read keeps the sampler hot path
--     single-query.
--   * The reaper consults mode at every tick; a partial index on
--     (app_id, mode) WHERE mode = 'mirror' keeps that lookup
--     O(log n_mirror) — the inverse of the common case (most
--     instances are normal). The sampler and reaper skip mirrors
--     via the in-process Go predicate; the index exists for the
--     reverse lookup "show me all mirrors for app X" used by the
--     dashboard's mirror panel (follow-on ADR-125 §follow-on 2).
--
-- Default 'normal' + CHECK {normal, mirror}:
--   * Pre-feature rows backfill to 'normal' — no behaviour change
--     for any existing customer.
--   * The CHECK is intentionally tight (2 values) so a typo from
--     a future contributor lands as SQLSTATE 23514 at write time
--     rather than leaking as a wire-shape typo on the wake proto.
--   * A widening migration that adds a 3rd value (e.g.
--     'preview_shadow' for PR previews per ADR-094) MUST carry
--     'normal' + 'mirror' forward — same invariant the edge_rules
--     kind CHECK carries (see 00265 regression fix in 00345).
--
-- Why mode='mirror' VMs are NOT billed:
--   * spec §4.7: billing = (plan_ram_mb + 8) × billed_seconds.
--     A mirror VM never serves the customer — the customer only
--     saw the source deployment's response — so charging for it
--     would be a customer-trust bug, not a feature.
--   * Sampler (pkg/meter/sampler.go:369-372) extends the existing
--     `!CountsForRAM()` skip with `Mode != 'mirror'`. The same
--     predicate appears in the reaper (pkg/sched/reaper.go:296)
--     because mirror VMs self-park on request completion — they
--     have no idle lifetime to reap.
--   * The 30-day instance retention sweep (pkg/sched.Retention)
--     needs no change: a PARKED mirror row has the same retention
--     semantics as a PARKED normal row.
--
-- Why mode lives on instances and not invocations:
--   * invocations is a per-request ledger (migrations/00030). The
--     customer-facing per-app summary aggregates by source. Adding
--     a 'mirror_invocation' source value would conflate customer
--     billing with internal observability — the billing aggregator
--     already excludes non-customer invocations via the per-row
--     `source` enum, but the meter side never sees invocations at
--     all (it reads usage_minutes, not invocations). mode='mirror'
--     is the right axis because meter reads instances, not
--     invocations.

ALTER TABLE instances
    ADD COLUMN IF NOT EXISTS mode text NOT NULL DEFAULT 'normal'
        CHECK (mode IN ('normal', 'mirror'));

CREATE INDEX IF NOT EXISTS instances_mode_idx
    ON instances (app_id, mode)
    WHERE mode = 'mirror';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS instances_mode_idx;
ALTER TABLE instances DROP COLUMN IF EXISTS mode;
-- +goose StatementEnd
