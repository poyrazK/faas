-- +goose Up
-- Stuck-running build sweep (issue #195 B1.4). The builderd reaper
-- sweeps `builds` rows stuck in status='running' past a 15-minute
-- threshold and flips them to status='failed' with
-- failure_class='timeout'. Without a partial index the sweep query
-- is a full table scan; with 10k builds per deployment cycle, that
-- scan dominates the EX44's pg cpu.
--
-- Partial index on (started_at) WHERE status='running' keeps the
-- sweep O(matches) instead of O(table). Mirrors
-- migrations/00017_instances_terminal_at.sql.
--
-- ADR-031 reconciliation (TODO for future ADR-031 v2): the spec says
-- stuck-running should requeue to 'queued'; the explicit issue #195
-- requires terminal 'failed' + 'timeout'. We follow the issue; the
-- future ADR will reconcile.
create index builds_running_started_idx
    on builds (started_at)
    where status = 'running';

-- +goose Down
drop index if exists builds_running_started_idx;
