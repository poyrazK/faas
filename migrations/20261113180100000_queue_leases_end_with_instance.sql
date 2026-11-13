-- filename: 20261113180100000_queue_leases_end_with_instance.sql
-- migration-compat: expand
-- ADR-20261113180000 (R6-4): a pull lease names the worker instance that
-- took it. When that instance stops or fails, nothing will settle its
-- leases, yet each held its message until the visibility timeout ran out.
-- The lease now ends with the instance: its expiry moves to now, and the
-- lease reaper returns the message to pending or dead-letters it on its last
-- attempt, as for any lapsed lease. The attempt the lease took stays spent:
-- a worker that died while handling the message may have died of it. Parked
-- and frozen instances keep their leases, since a restore resumes their
-- handlers.

-- +goose Up
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION end_queue_leases_with_instance() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF NEW.app_id IS NULL THEN
    RETURN NEW;
  END IF;
  UPDATE invocations i SET lease_expires_at = clock_timestamp()
  WHERE i.app_id = NEW.app_id AND i.source = 'queue' AND i.state = 'dispatching'
    AND i.instance_id = NEW.id::text AND i.lease_expires_at > clock_timestamp()
    AND EXISTS (SELECT 1 FROM invocation_queue_lease_receipts r
      WHERE r.invocation_id = i.id AND r.attempt = i.attempts AND r.replay_generation = i.replay_generation);
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER instance_end_queue_leases AFTER UPDATE OF state ON instances
    FOR EACH ROW WHEN ((old.state IS DISTINCT FROM new.state) AND (new.state = ANY (ARRAY['stopped'::text, 'failed'::text])))
    EXECUTE FUNCTION end_queue_leases_with_instance();

-- +goose Down
DROP TRIGGER IF EXISTS instance_end_queue_leases ON instances;
DROP FUNCTION IF EXISTS end_queue_leases_with_instance();
