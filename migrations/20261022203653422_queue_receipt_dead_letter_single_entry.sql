-- filename: 20261022203653422_queue_receipt_dead_letter_single_entry.sql

-- ADR-20261022203653: one failed queue or delayed-task message is one entry in
-- the unified dead-letter list. Its invocation is that entry. The message's
-- push receipt (a trigger record of a kind='queue' trigger) is still captured
-- into dead_letter_events and trigger_dead_letter for audit, but the
-- production view no longer lists it, so list, inspect, replay-all and purge
-- address the message once. origin is the trigger kind captured at failure, so
-- the predicate survives the trigger's deletion (trigger_id is SET NULL then).
-- Only the WHERE clause changes; the columns stay the same, so the previous
-- release reads this view unchanged.
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE OR REPLACE VIEW production_dead_letter_events AS SELECT e.* FROM dead_letter_events e
WHERE NOT e.environment_owned AND NOT (e.source='trigger_record' AND e.origin='queue')
    AND (e.source<>'invocation' OR (NOT faas_invocation_headers_own_stage(e.app_id,e.headers)
    AND NOT EXISTS(SELECT 1 FROM invocations i WHERE i.id=e.source_id
        AND NOT EXISTS(SELECT 1 FROM production_invocation_work p WHERE p.id=i.id))));

-- +goose Down
SET LOCAL lock_timeout = '5s';

CREATE OR REPLACE VIEW production_dead_letter_events AS SELECT e.* FROM dead_letter_events e
WHERE NOT e.environment_owned AND (e.source<>'invocation' OR (NOT faas_invocation_headers_own_stage(e.app_id,e.headers)
    AND NOT EXISTS(SELECT 1 FROM invocations i WHERE i.id=e.source_id
        AND NOT EXISTS(SELECT 1 FROM production_invocation_work p WHERE p.id=i.id))));
