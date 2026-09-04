-- filename: 00577_instances_mode_job.sql
-- +goose Up
-- +goose StatementBegin
--
-- Issue #1184 Workstream A / ADR-099 supplement: Mega-1 jobs.
-- Widen `instances.mode` CHECK to include 'job' so a job-task VM
-- row can be stamped at CreateInstanceWithMode time. The meterd
-- sampler + schedd reaper consult `mode` to skip non-billable rows
-- (mirror) — a job-task VM IS billable, so it sits next to
-- 'normal' in the sampler path; the only mode-specific behaviour
-- for 'job' is the JobConcurrentByAccount gate (uses kind=
-- 'job_task' on instances, not mode), so widening the mode column
-- doesn't change the billing / reaper predicates.
--
-- Why a new migration (not 00385 amend): 00385 carries the mirror
-- semantics (why-mirror-not-billed, partial-index explanation,
-- retention-sweep non-impact) and rewriting it would orphan those
-- code references. A widening migration is the same shape as
-- 00345's edge_rules kind widening (carry the existing values
-- forward, add the new one) — both are checked at write time by
-- the same NOT VALID → VALIDATE pattern.
--
-- The migration does NOT add a partial index for mode='job' the
-- way 00385 adds one for mode='mirror': job rows are common (not
-- inverse-of-common) and the existing instance-kind partial
-- indexes already serve the per-job lookup. mode='job' is
-- observability-only at the dashboard layer.
--
-- M-2 (PR #1202) widens further to include 'worker' and 'service'
-- (ADR-137 execution-mode taxonomy). The CHECK here lands as
-- {normal, mirror, job, worker, service} so the M-2 deployment
-- ordering — 00570 first widens to the superset, 00577 idempotently
-- re-widens to the same superset — leaves the final shape correct.
-- Without the M-2 widening, 00577 would TIGHTEN from 00570's
-- closed set down to {normal, mirror, job}, breaking mode='worker'
-- and mode='service' inserts at runtime.

ALTER TABLE instances
    DROP CONSTRAINT IF EXISTS instances_mode_check;

ALTER TABLE instances
    ADD CONSTRAINT instances_mode_check
    CHECK (mode IN ('normal', 'mirror', 'job', 'worker', 'service')) NOT VALID;

ALTER TABLE instances
    VALIDATE CONSTRAINT instances_mode_check;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- The down-migrate restores the 00385-era CHECK set. A clean
-- reverse (apply 00531, then down) leaves the DB on the 00385
-- set. A rollback on a database with rows in mode='job' will
-- fail — schedd would have to rewrite those rows back to
-- 'normal' first. The Mega-1 release notes call this out as a
-- one-way migration for the duration of the jobs ramp.
--
-- M-2 (PR #1202) extends the down-migrate's restoration target to
-- the M-2 superset (matches the up-migrate above). Going further
-- down (past M-2) requires 00570's down-migrate first, which
-- rewrites worker/service/job rows back to 'normal'.
ALTER TABLE instances
    DROP CONSTRAINT IF EXISTS instances_mode_check;

ALTER TABLE instances
    ADD CONSTRAINT instances_mode_check
    CHECK (mode IN ('normal', 'mirror', 'job', 'worker', 'service'));
-- +goose StatementEnd
