-- Event plan caps micro-benchmark (ADR-20261023215834). Run against a
-- fresh database loaded from the branch's schema.sql.
\set ON_ERROR_STOP on
\pset pager off
\timing on

-- One heavy account at 1,048,576 retained receipts (today's Scale budget),
-- 200 light owners with 1,000 each, all settled.
INSERT INTO accounts(id, email, plan) VALUES ('00000000-0000-0000-0000-00000000a000', 'heavy@bench.example', 'scale');
INSERT INTO accounts(id, email, plan)
SELECT ('00000000-0000-0000-0000-' || lpad(to_hex(n), 12, '0'))::uuid, 'light' || n || '@bench.example',
       (ARRAY['free','hobby','pro','scale'])[1 + n % 4]
FROM generate_series(1, 200) n;
INSERT INTO resource_namespaces(id) SELECT id FROM accounts ON CONFLICT DO NOTHING;

SET session_replication_role = replica;
INSERT INTO event_fanout_outbox(account_id, source, event_id, event_type, event_data, payload, recipient_snapshot, state, created_at, delivered_at)
SELECT '00000000-0000-0000-0000-00000000a000', 'orders', 'heavy-' || n, 'created', jsonb_build_object('order', n, 'note', repeat('x', 120)),
       jsonb_build_object('id', 'heavy-' || n, 'source', 'orders', 'type', 'created', 'data', jsonb_build_object('order', n, 'note', repeat('x', 120))),
       '[{"id":"s","app_id":"a"}]'::jsonb, 'delivered', now() - interval '6 days' + n * interval '400 milliseconds', now() - interval '6 days' + n * interval '400 milliseconds'
FROM generate_series(1, 1048576) n;
INSERT INTO event_fanout_outbox(account_id, source, event_id, event_type, event_data, payload, recipient_snapshot, state, created_at, delivered_at)
SELECT a.id, 'orders', 'light-' || n, 'created', '{"n":1}'::jsonb,
       jsonb_build_object('id', 'light-' || n, 'source', 'orders', 'type', 'created', 'data', '{"n":1}'::jsonb), '[]'::jsonb,
       'delivered', now() - interval '20 days' + n * interval '20 minutes', now() - interval '20 days' + n * interval '20 minutes'
FROM accounts a CROSS JOIN generate_series(1, 1000) n WHERE a.email LIKE 'light%';
SET session_replication_role = origin;
INSERT INTO event_receipt_usage(account_id, retained_events, retained_bytes)
SELECT account_id, count(*) FILTER (WHERE customer_storage_bytes > 0), coalesce(sum(customer_storage_bytes), 0)
FROM event_fanout_outbox GROUP BY account_id;
VACUUM (ANALYZE) event_fanout_outbox;
VACUUM (ANALYZE) event_receipt_usage;
SELECT pg_size_pretty(pg_total_relation_size('event_fanout_outbox')) AS outbox_size;

\echo === admission read, main: count every retained receipt (5 runs)
SELECT count(*)::bigint, coalesce(sum(customer_storage_bytes),0)::bigint FROM event_fanout_outbox WHERE account_id='00000000-0000-0000-0000-00000000a000' AND customer_storage_bytes>0;
SELECT count(*)::bigint, coalesce(sum(customer_storage_bytes),0)::bigint FROM event_fanout_outbox WHERE account_id='00000000-0000-0000-0000-00000000a000' AND customer_storage_bytes>0;
SELECT count(*)::bigint, coalesce(sum(customer_storage_bytes),0)::bigint FROM event_fanout_outbox WHERE account_id='00000000-0000-0000-0000-00000000a000' AND customer_storage_bytes>0;
SELECT count(*)::bigint, coalesce(sum(customer_storage_bytes),0)::bigint FROM event_fanout_outbox WHERE account_id='00000000-0000-0000-0000-00000000a000' AND customer_storage_bytes>0;
EXPLAIN (ANALYZE, BUFFERS) SELECT count(*)::bigint, coalesce(sum(customer_storage_bytes),0)::bigint FROM event_fanout_outbox WHERE account_id='00000000-0000-0000-0000-00000000a000' AND customer_storage_bytes>0;

\echo === admission read, branch: the usage counter (5 runs)
SELECT coalesce(max(retained_events),0)::bigint, coalesce(max(retained_bytes),0)::bigint FROM event_receipt_usage WHERE account_id='00000000-0000-0000-0000-00000000a000';
SELECT coalesce(max(retained_events),0)::bigint, coalesce(max(retained_bytes),0)::bigint FROM event_receipt_usage WHERE account_id='00000000-0000-0000-0000-00000000a000';
SELECT coalesce(max(retained_events),0)::bigint, coalesce(max(retained_bytes),0)::bigint FROM event_receipt_usage WHERE account_id='00000000-0000-0000-0000-00000000a000';
SELECT coalesce(max(retained_events),0)::bigint, coalesce(max(retained_bytes),0)::bigint FROM event_receipt_usage WHERE account_id='00000000-0000-0000-0000-00000000a000';
EXPLAIN (ANALYZE, BUFFERS) SELECT coalesce(max(retained_events),0)::bigint, coalesce(max(retained_bytes),0)::bigint FROM event_receipt_usage WHERE account_id='00000000-0000-0000-0000-00000000a000';

\echo === prune owner walk: 201 owners, light receipts span 20 days
PREPARE walk(timestamptz) AS
WITH plan_retention AS (
 SELECT unnest('{free,hobby,pro,scale}'::text[]) AS plan, unnest('{86400,259200,604800,1209600}'::bigint[]) AS seconds
)
SELECT u.account_id, w.before_at
FROM event_receipt_usage u
LEFT JOIN accounts a ON a.id=u.account_id
LEFT JOIN orgs g ON g.id=u.account_id AND NOT g.personal_org AND g.resource_ownership_enabled
LEFT JOIN plan_retention r ON r.plan=coalesce(a.plan,g.plan)
CROSS JOIN LATERAL (SELECT $1-make_interval(secs=>coalesce(r.seconds,1209600)) AS before_at) w
CROSS JOIN LATERAL (
 SELECT o.delivered_at FROM event_fanout_outbox o
 WHERE o.account_id=u.account_id AND o.state='delivered' AND o.delivered_at<w.before_at
  AND event_receipt_retention_hold(o.account_id,o.id,o.created_at,$1-interval '30 days',$1)=''
 ORDER BY o.delivered_at,o.id LIMIT 1
) due
ORDER BY due.delivered_at,u.account_id LIMIT 100;
EXPLAIN (ANALYZE, BUFFERS) EXECUTE walk(now());
EXPLAIN (ANALYZE, BUFFERS) EXECUTE walk(now());
\echo nothing due anywhere (every receipt inside its window)
EXPLAIN (ANALYZE, BUFFERS) EXECUTE walk(now() - interval '40 days');

\echo === main prune candidate scan for comparison (fixed 30-day cutoff)
EXPLAIN (ANALYZE, BUFFERS)
WITH candidates AS MATERIALIZED (
 SELECT o.account_id,o.delivered_at,o.id FROM event_fanout_outbox o
 WHERE o.state='delivered' AND o.delivered_at<now()-interval '30 days'
  AND event_receipt_retention_hold(o.account_id,o.id,o.created_at,now()-interval '30 days',now())=''
 ORDER BY o.delivered_at,o.id LIMIT 100
)
SELECT account_id FROM candidates GROUP BY account_id ORDER BY min(delivered_at),account_id;

\echo === per-publish trigger cost: 5,000 single-row customer inserts, counters on vs off
CREATE OR REPLACE PROCEDURE bench_publish(prefix text, n int) LANGUAGE plpgsql AS $$
BEGIN
 FOR i IN 1..n LOOP
  INSERT INTO event_fanout_outbox(account_id, source, event_id, event_type, event_data, payload)
  VALUES ('00000000-0000-0000-0000-00000000a000', 'orders', prefix || i, 'created', '{"n":1}'::jsonb,
          jsonb_build_object('id', prefix || i, 'source', 'orders', 'type', 'created', 'data', '{"n":1}'::jsonb));
  COMMIT;
 END LOOP;
END $$;
CALL bench_publish('warm-', 1000);
CALL bench_publish('on-a-', 5000);
ALTER TABLE event_fanout_outbox DISABLE TRIGGER event_receipt_usage_insert;
ALTER TABLE event_fanout_outbox DISABLE TRIGGER event_receipt_usage_charge;
CALL bench_publish('off-a-', 5000);
ALTER TABLE event_fanout_outbox ENABLE TRIGGER event_receipt_usage_insert;
ALTER TABLE event_fanout_outbox ENABLE TRIGGER event_receipt_usage_charge;
CALL bench_publish('on-b-', 5000);
ALTER TABLE event_fanout_outbox DISABLE TRIGGER event_receipt_usage_insert;
ALTER TABLE event_fanout_outbox DISABLE TRIGGER event_receipt_usage_charge;
CALL bench_publish('off-b-', 5000);
ALTER TABLE event_fanout_outbox ENABLE TRIGGER event_receipt_usage_insert;
ALTER TABLE event_fanout_outbox ENABLE TRIGGER event_receipt_usage_charge;

\echo === prune batch cost: one 100-receipt delete, statement trigger on vs off
BEGIN;
DELETE FROM event_fanout_outbox WHERE id IN (SELECT id FROM event_fanout_outbox WHERE account_id='00000000-0000-0000-0000-00000000a000' AND state='delivered' ORDER BY delivered_at, id LIMIT 100);
ROLLBACK;
BEGIN;
DELETE FROM event_fanout_outbox WHERE id IN (SELECT id FROM event_fanout_outbox WHERE account_id='00000000-0000-0000-0000-00000000a000' AND state='delivered' ORDER BY delivered_at, id LIMIT 100);
ROLLBACK;
ALTER TABLE event_fanout_outbox DISABLE TRIGGER event_receipt_usage_delete;
BEGIN;
DELETE FROM event_fanout_outbox WHERE id IN (SELECT id FROM event_fanout_outbox WHERE account_id='00000000-0000-0000-0000-00000000a000' AND state='delivered' ORDER BY delivered_at, id LIMIT 100);
ROLLBACK;
BEGIN;
DELETE FROM event_fanout_outbox WHERE id IN (SELECT id FROM event_fanout_outbox WHERE account_id='00000000-0000-0000-0000-00000000a000' AND state='delivered' ORDER BY delivered_at, id LIMIT 100);
ROLLBACK;
ALTER TABLE event_fanout_outbox ENABLE TRIGGER event_receipt_usage_delete;

\echo === counters after the bench: exactly the 10,000 inserts made with the counters off are uncounted
SELECT c.events - u.retained_events AS uncounted_events, u.retained_events, c.events
FROM event_receipt_usage u
JOIN (SELECT account_id, count(*) FILTER (WHERE customer_storage_bytes > 0) AS events, coalesce(sum(customer_storage_bytes),0) AS bytes FROM event_fanout_outbox GROUP BY account_id) c USING (account_id)
WHERE u.account_id='00000000-0000-0000-0000-00000000a000';
