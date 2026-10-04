import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import pg from 'pg';
import { insertCommitEvent } from '../dist/index.js';

const { Client } = pg;
const dsn = process.env.DATABASE_URL;

test('Commit helper shares the business transaction and pins the public outbox', { skip: !dsn, timeout: 30000 }, async () => {
  const admin = new Client({ connectionString: dsn, connectionTimeoutMillis: 5000, query_timeout: 10000 });
  const database = `commit_sdk_node_${randomUUID().replaceAll('-', '')}`;
  let writer, observer, created = false;
  await admin.connect();
  try {
    await admin.query(`CREATE DATABASE "${database}" TEMPLATE template0`);
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
  } finally {
    if (writer) await writer.end();
    if (observer) await observer.end();
    try { if (created) await admin.query(`DROP DATABASE "${database}" WITH (FORCE)`); }
    finally { await admin.end(); }
  }
});
