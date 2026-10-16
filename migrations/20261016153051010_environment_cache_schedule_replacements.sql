-- filename: 20261016153051010_environment_cache_schedule_replacements.sql

-- +goose Up
ALTER TABLE environment_cache_schedules DROP CONSTRAINT IF EXISTS environment_cache_schedule_replaces;
ALTER TABLE environment_cache_schedules DROP CONSTRAINT IF EXISTS environment_cache_schedule_replaced;
ALTER TABLE environment_cache_schedules
 ADD COLUMN IF NOT EXISTS replaces_schedule_id uuid UNIQUE REFERENCES environment_cache_schedules(schedule_id) DEFERRABLE INITIALLY DEFERRED,
 ADD COLUMN IF NOT EXISTS replaced_by_schedule_id uuid UNIQUE REFERENCES environment_cache_schedules(schedule_id) DEFERRABLE INITIALLY DEFERRED,
 ADD CONSTRAINT environment_cache_schedule_replaces CHECK (replaces_schedule_id IS NULL OR replaces_schedule_id<>schedule_id),
 ADD CONSTRAINT environment_cache_schedule_replaced CHECK (replaced_by_schedule_id IS NULL OR (replaced_by_schedule_id<>schedule_id AND status='cancelled'));
CREATE INDEX IF NOT EXISTS environment_cache_schedules_discovery ON environment_cache_schedules(account_id,app_id,environment_id,execute_at,schedule_id);
-- +goose Down
DROP INDEX environment_cache_schedules_discovery;
ALTER TABLE environment_cache_schedules DROP CONSTRAINT environment_cache_schedule_replaces, DROP CONSTRAINT environment_cache_schedule_replaced, DROP COLUMN replaces_schedule_id, DROP COLUMN replaced_by_schedule_id;
