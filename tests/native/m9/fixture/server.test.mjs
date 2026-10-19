// spec: §14.A
// adr: 1530
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { once } from 'node:events';
import test from 'node:test';
import { fixtureServer } from './server.mjs';

async function start(t, fixtureID) {
  const server = fixtureServer(fixtureID).listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise((resolve) => { server.closeAllConnections(); server.close(resolve); }));
  return `http://127.0.0.1:${server.address().port}/probe`;
}

async function probe(url, fixtureID, operationID) {
  return fetch(url, {method: 'POST', body: JSON.stringify({fixture_id: fixtureID, operation_id: operationID})});
}

test('replayed operations retain one in-memory sequence; cold boots lose proof', async (t) => {
  const fixture = randomUUID(), operation = randomUUID();
  const url = await start(t, fixture);
  const first = await (await probe(url, fixture, operation)).json();
  assert.equal(first.sequence, 1);
  assert.deepEqual(await (await probe(url, fixture, operation)).json(), first);
  const second = await (await probe(url, fixture, randomUUID())).json();
  assert.equal(second.unique_operations, 2);
  assert.equal(second.sequence, 2);
  assert.equal(second.boot_id, first.boot_id);
  const coldURL = await start(t, fixture);
  const cold = await (await probe(coldURL, fixture, operation)).json();
  assert.notEqual(cold.boot_id, first.boot_id);
  assert.equal(cold.unique_operations, 1);
});

test('foreign fixtures, invalid operations and oversized bodies produce no receipts', async (t) => {
  const fixture = randomUUID(), url = await start(t, fixture);
  assert.equal((await probe(url, randomUUID(), randomUUID())).status, 400);
  assert.equal((await probe(url, fixture, '------------------------000000000000')).status, 400);
  assert.equal((await fetch(url, {method: 'POST', body: 'x'.repeat(2048)})).status, 400);
  const first = await (await probe(url, fixture, randomUUID())).json();
  assert.equal(first.unique_operations, 1);
});
