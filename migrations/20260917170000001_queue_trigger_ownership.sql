-- +goose Up
-- One enabled in-platform queue trigger owns one (app, source) stream.
-- This keeps queue and delayed_task rows single-consumer when schedd runs
-- both the trigger dispatcher and the legacy invocation drain.
UPDATE triggers
   SET source = config->>'mode'
 WHERE kind = 'queue'
   AND source IS NULL
   AND config->>'mode' IN ('queue', 'delayed_task');

CREATE UNIQUE INDEX IF NOT EXISTS triggers_one_enabled_queue_source
    ON triggers (app_id, source)
    WHERE kind = 'queue' AND enabled AND source IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS triggers_one_enabled_queue_source;
