-- +goose Up
ALTER TABLE environment_cache_schedule_event_counters
 ADD COLUMN pruned_through bigint NOT NULL DEFAULT 0 CHECK (pruned_through>=0 AND pruned_through<=last_sequence);
-- +goose Down
ALTER TABLE environment_cache_schedule_event_counters DROP COLUMN pruned_through;
