import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import pg from 'pg';
import { insertCommitEvent, insertRealtimeCommitEvent, listRealtimeCommitDeliveries, getRealtimeCommitDelivery, retryRealtimeCommitEvent } from '../dist/index.js';

const { Client } = pg;
const dsn = process.env.DATABASE_URL;

test('Commit helper shares the business transaction and pins the public outbox', { skip: !dsn, timeout: 30000 }, async () => {
  const admin = new Client({ connectionString: dsn, connectionTimeoutMillis: 5000, query_timeout: 10000 });
  const database = `commit_sdk_node_${randomUUID().replaceAll('-', '')}`;
  let writer, observer, created = false;
  await admin.connect();
  try {
    await admin.query(`CREATE DATABASE "${database}" TEMPLATE template0 ENCODING 'UTF8'`);
    created = true;
    const url = new URL(dsn);
    url.pathname = `/${database}`;
    writer = new Client({ connectionString: url.toString(), connectionTimeoutMillis: 5000, query_timeout: 10000 });
    observer = new Client({ connectionString: url.toString(), connectionTimeoutMillis: 5000, query_timeout: 10000 });
    await writer.connect();
    await observer.connect();
    const ddl = await readFile(new URL('../../../pkg/commit/schema.sql', import.meta.url), 'utf8');
    await writer.query(ddl);
    await writer.query('CREATE SCHEMA business; CREATE TABLE business.orders(id integer PRIMARY KEY); CREATE TABLE business.gregale_outbox(LIKE public.gregale_outbox INCLUDING ALL)');
    await writer.query('SET search_path=business,public');
    const counts = async () => (await observer.query('SELECT (SELECT count(*)::int FROM business.orders) AS orders, (SELECT count(*)::int FROM public.gregale_outbox) AS events')).rows[0];
    await writer.query('BEGIN');
    await writer.query('INSERT INTO orders VALUES(1)');
    const identity = await insertCommitEvent(writer, { type: 'order.created', data: { order_id: 1 } });
    assert.deepEqual(await counts(), { orders: 0, events: 0 });
    await writer.query('COMMIT');
    assert.deepEqual(await counts(), { orders: 1, events: 1 });
    assert.deepEqual((await observer.query('SELECT event_id::text,event_type,payload FROM public.gregale_outbox')).rows[0], { event_id: identity, event_type: 'order.created', payload: { order_id: 1 } });
    await writer.query('BEGIN');
    await writer.query('INSERT INTO orders VALUES(2)');
    await insertCommitEvent(writer, { type: 'order.created', data: { order_id: 2 } });
    await writer.query('ROLLBACK');
    assert.deepEqual(await counts(), { orders: 1, events: 1 });
    await writer.query('BEGIN');
    await writer.query('INSERT INTO orders VALUES(3)');
    await assert.rejects(insertCommitEvent(writer, { id: identity, type: 'order.changed', data: { order_id: 3 } }), { code: '23505' });
    await writer.query('ROLLBACK');
    assert.deepEqual(await counts(), { orders: 1, events: 1 });
    await writer.query('BEGIN');
    await writer.query('INSERT INTO orders VALUES(4)');
    await assert.rejects(insertCommitEvent(writer, { type: 'order.created', data: 1n }), TypeError);
    await writer.query('ROLLBACK');
    assert.deepEqual(await counts(), { orders: 1, events: 1 });
    assert.equal((await observer.query('SELECT count(*)::int AS events FROM business.gregale_outbox')).rows[0].events, 0);
    const customer = randomUUID();
    const routing = { version: 2, platform_tenant_id: customer.toUpperCase(), key: 'order-5' };
    await writer.query('BEGIN');
    await writer.query('INSERT INTO orders VALUES(5)');
    const routed = await insertCommitEvent(writer, { type: 'order.created', data: { order_id: 5 }, routing });
    assert.deepEqual(await counts(), { orders: 1, events: 1 });
    await writer.query('COMMIT');
    assert.deepEqual(await counts(), { orders: 2, events: 2 });
    assert.deepEqual((await observer.query('SELECT routing FROM public.gregale_outbox WHERE event_id=$1', [routed])).rows[0].routing,
      { ...routing, platform_tenant_id: customer });
    await writer.query('BEGIN');
    await insertCommitEvent(writer, { type: 'order.created', data: {}, routing });
    await writer.query('ROLLBACK');
    assert.deepEqual(await counts(), { orders: 2, events: 2 });
    await assert.rejects(insertCommitEvent(writer, { type: 'order.created', data: {}, routing: { version: 2, key: null } }), TypeError);
    await writer.query(await readFile(new URL('../../../pkg/commit/realtime_schema.sql', import.meta.url), 'utf8'));
    const endpointId = '0123456789abcdef0123456789abcdef';
    await writer.query('BEGIN');
    await writer.query('INSERT INTO orders VALUES(6)');
    const realtimeId = await insertRealtimeCommitEvent(writer, { endpointId, channel: 'orders', data: { order_id: 6 } });
    assert.equal(await getRealtimeCommitDelivery(observer, realtimeId), null);
    await writer.query('ROLLBACK');
    assert.equal(await getRealtimeCommitDelivery(observer, realtimeId), null);
    assert.deepEqual(await counts(), { orders: 2, events: 2 });
    await writer.query('BEGIN');
    await writer.query('INSERT INTO orders VALUES(6)');
    await insertRealtimeCommitEvent(writer, { id: realtimeId, endpointId, channel: 'orders', data: { order_id: 6 } });
    await writer.query('COMMIT');
    assert.equal((await getRealtimeCommitDelivery(observer, realtimeId)).status, 'pending');
    assert.equal((await listRealtimeCommitDeliveries(observer))[0].id, realtimeId);
    await writer.query("UPDATE public.gregale_realtime_outbox SET blocked_code='destination_unqualified' WHERE event_id=$1", [realtimeId]);
    assert.equal((await getRealtimeCommitDelivery(observer, realtimeId)).status, 'blocked');
    await retryRealtimeCommitEvent(writer, realtimeId);
    assert.equal((await getRealtimeCommitDelivery(observer, realtimeId)).status, 'pending');
    await writer.query('UPDATE public.gregale_realtime_outbox SET published_at=clock_timestamp(),sequence=9007199254740993 WHERE event_id=$1', [realtimeId]);
    const delivery = await getRealtimeCommitDelivery(observer, realtimeId);
    assert.equal(delivery.status, 'published');
    assert.equal(delivery.sequence, '9007199254740993');
    assert.equal('data' in delivery, false);
    await retryRealtimeCommitEvent(writer, realtimeId);
    assert.equal((await getRealtimeCommitDelivery(observer, realtimeId)).status, 'published');

  } finally {
    if (writer) await writer.end();
    if (observer) await observer.end();
    try { if (created) await admin.query(`DROP DATABASE "${database}" WITH (FORCE)`); }
    finally { await admin.end(); }
  }
});
