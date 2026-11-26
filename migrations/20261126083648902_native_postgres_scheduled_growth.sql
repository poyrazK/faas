-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- A catalog reservation pins the policy and running incarnation that
-- qualified. Only schedd can admit the actual stop and reserve node space.
CREATE TABLE IF NOT EXISTS native_postgres_scheduled_growth (
    resize_id uuid PRIMARY KEY REFERENCES managed_postgres_resizes(id) ON DELETE CASCADE,
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    source_running_at timestamptz NOT NULL,
    policy_at timestamptz NOT NULL
);

ALTER TABLE native_postgres_base_refreshes
    ADD COLUMN IF NOT EXISTS resize_id uuid UNIQUE REFERENCES native_postgres_scheduled_growth(resize_id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS target_volume_bytes bigint,
    ADD COLUMN IF NOT EXISTS refresh_base boolean NOT NULL DEFAULT true;

-- UTC arithmetic is performed before converting back to timestamptz.
-- +goose StatementBegin
DO $create_function$
BEGIN
    IF to_regprocedure('native_postgres_open_window(integer, integer, integer, timestamptz)') IS NULL THEN
        CREATE FUNCTION native_postgres_open_window(weekday integer, start_minute integer, duration_minutes integer, at_time timestamptz)
        RETURNS timestamptz LANGUAGE sql IMMUTABLE STRICT
        AS $$
            WITH clock AS (SELECT at_time AT TIME ZONE 'UTC' AS now_utc),
            opening AS (
                SELECT now_utc, date_trunc('day', now_utc)
                  - make_interval(days => (extract(dow FROM now_utc)::int - weekday + 7) % 7)
                  + make_interval(mins => start_minute) AS today FROM clock
            ), latest AS (
                SELECT now_utc, CASE WHEN today > now_utc THEN today - interval '7 days' ELSE today END AS opened
                FROM opening
            )
            SELECT opened AT TIME ZONE 'UTC' FROM latest
            WHERE now_utc < opened + make_interval(mins => duration_minutes)
        $$;
    END IF;
END;
$create_function$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION native_postgres_auto_grow_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.volume_bytes > OLD.volume_bytes AND EXISTS (
        SELECT 1 FROM managed_postgres_resizes r
        LEFT JOIN native_postgres_auto_grow p ON p.database_id = r.database_id
        WHERE r.provider_resource_id = OLD.database_id::text
            AND r.state = 'pending' AND r.automatic AND r.target_storage_limit_bytes = NEW.volume_bytes
            AND (OLD.state <> 'parked' OR OLD.replicated OR
                CASE WHEN OLD.always_on THEN NOT EXISTS (
                    SELECT 1 FROM native_postgres_base_refreshes a
                    WHERE a.resize_id = r.id AND a.database_id = OLD.database_id
                        AND a.node_id = OLD.node_id AND a.finished_at IS NULL
                        AND a.target_volume_bytes = NEW.volume_bytes
                ) ELSE (
                    OLD.held OR OLD.idle_parked_at IS DISTINCT FROM OLD.state_changed_at
                    OR OLD.idle_parked_at IS DISTINCT FROM r.auto_grow_idle_at
                    OR p.max_bytes IS NULL OR p.max_bytes < NEW.volume_bytes
                ) END)
    ) THEN
        RAISE EXCEPTION 'automatic storage growth requires an authorized park'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.state = 'waking' AND (
        EXISTS (
            SELECT 1 FROM native_postgres_base_refreshes a
            WHERE a.database_id = OLD.database_id AND a.finished_at IS NULL
                AND a.resize_id IS NOT NULL AND a.target_volume_bytes > OLD.volume_bytes
        ) OR (OLD.resize_pending AND EXISTS (
            SELECT 1 FROM managed_postgres_resizes r
            WHERE r.provider_resource_id = OLD.database_id::text
                AND r.state = 'pending' AND r.automatic AND r.target_storage_limit_bytes = OLD.volume_bytes
        ))
    ) THEN
        RAISE EXCEPTION 'automatic storage growth must finish before waking'
            USING ERRCODE = 'lock_not_available';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
