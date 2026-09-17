-- +goose Up
-- Central-mode gateways now atomically consume the shared Postgres bucket on
-- every request. The former per-process fast-path cache is no longer an
-- admission authority, so per-consume NOTIFY traffic only adds database and
-- listener load and can erase the local degraded-mode/header mirror.
DROP TRIGGER IF EXISTS pg_ratelimit_counters_notify ON pg_ratelimit_counters;
DROP FUNCTION IF EXISTS notify_pg_ratelimit_counters();

-- +goose Down
CREATE OR REPLACE FUNCTION notify_pg_ratelimit_counters() RETURNS trigger AS $$
BEGIN
  PERFORM pg_notify(
    'rate_limit_changed',
    json_build_object(
      'scope', NEW.scope,
      'subject_id', NEW.subject_id,
      'plan', NEW.plan
    )::text
  );
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS pg_ratelimit_counters_notify ON pg_ratelimit_counters;
CREATE TRIGGER pg_ratelimit_counters_notify
  AFTER INSERT OR UPDATE OF tokens, last_refill ON pg_ratelimit_counters
  FOR EACH ROW EXECUTE FUNCTION notify_pg_ratelimit_counters();
