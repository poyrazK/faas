-- +goose Up
-- +goose StatementBegin

-- ADR-030: build queue-wait telemetry. builderd observes
-- <daemon>_build_queue_wait_seconds = dequeue_time - enqueued_at so the
-- §12 dashboard row (target < 60 s, warn > 300 s) has a real source.
-- Before this column the builds table carried only started_at /
-- finished_at, so the time a build spent queued on the single guaranteed
-- builder slot was unobservable.
--
-- default now() backfills existing rows with their migration time; that
-- is a harmless one-time skew (no build predating this migration is
-- still queued) and keeps the column NOT NULL without a separate
-- backfill pass. apid's CreateBuild relies on the default at insert.

alter table builds
    add column enqueued_at timestamptz not null default now();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

alter table builds drop column enqueued_at;

-- +goose StatementEnd
