-- filename: 20261011224925696_gateway_concurrency_queue_lease_acquire.sql

-- Production-us hunt #9 (H9-76): 80 requests overflowing a saturated app
-- each ran the fleet queue-permit acquire as a client-side transaction:
-- BEGIN, advisory lock, reap, count, insert, COMMIT. The app-scoped lock was
-- held across five client round trips, so contenders serialized at that
-- latency while each held one of the gateway's eight pool connections. Every
-- acquire used the full 2 s queue budget, 69 requests failed with "queue
-- admission unavailable", and the same gateways' rate limiter and lease
-- releases timed out waiting for a connection.
--
-- The acquire now runs server-side in one statement. The lock is held only
-- for the function body plus its commit, and the caller holds a connection
-- for one round trip. Semantics are unchanged: same lock key, same reap,
-- same cap check. PL/pgSQL takes a fresh snapshot per statement, so the count
-- after the lock sees every lease a previous holder committed.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION gateway_try_acquire_concurrency_queue_lease(
    p_app_id text,
    p_lease_id uuid,
    p_limit integer,
    p_ttl_ms bigint,
    OUT admitted boolean,
    OUT depth integer
) LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('gateway-concurrency-queue:' || p_app_id, 0));
    DELETE FROM gateway_concurrency_queue_leases WHERE app_id = p_app_id::uuid AND expires_at <= now();
    SELECT count(*)::integer INTO depth FROM gateway_concurrency_queue_leases WHERE app_id = p_app_id::uuid;
    IF depth >= p_limit THEN
        admitted := false;
        RETURN;
    END IF;
    INSERT INTO gateway_concurrency_queue_leases (lease_id, app_id, expires_at)
    VALUES (p_lease_id, p_app_id::uuid, now() + p_ttl_ms * interval '1 millisecond');
    admitted := true;
    depth := depth + 1;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS gateway_try_acquire_concurrency_queue_lease(text, uuid, integer, bigint);
-- +goose StatementEnd
