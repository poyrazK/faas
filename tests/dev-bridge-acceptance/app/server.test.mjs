import assert from 'node:assert/strict';
import test from 'node:test';
import { once } from 'node:events';
import { fixtureServer } from './server.mjs';

test('fixture uses SDK propagation at both service hops and proves production hop denial', async t => {
  const observed = [];
  const authority = `account.${Buffer.alloc(32, 1).toString('base64url')}.${Buffer.alloc(32, 2).toString('base64url')}`;
  const inventory = fixtureServer({ role: 'inventory' });
  inventory.listen(0, '127.0.0.1');
  await once(inventory, 'listening');
  t.after(() => inventory.close());
  const mappedFetch = async input => {
    const request = new Request(input);
    observed.push([new URL(request.url).hostname, request.headers.get('X-Gregale-Dev-Session-Context')]);
    const port = new URL(request.url).hostname === 'payments.svc.gregale' ? payments.address().port : inventory.address().port;
    return fetch(`http://127.0.0.1:${port}${new URL(request.url).pathname}`, { headers: request.headers });
  };
  const payments = fixtureServer({ role: 'payments', fetchImpl: mappedFetch });
  payments.listen(0, '127.0.0.1');
  await once(payments, 'listening');
  t.after(() => payments.close());
  const frontend = fixtureServer({ role: 'frontend', fetchImpl: mappedFetch });
  frontend.listen(0, '127.0.0.1');
  await once(frontend, 'listening');
  t.after(() => frontend.close());
  const url = `http://127.0.0.1:${frontend.address().port}/charge`;
  for (const path of ['/', '/health', '/healthz']) {
    const health = await fetch(`http://127.0.0.1:${frontend.address().port}${path}`);
    assert.equal(health.status, 200);
    assert.deepEqual(await health.json(), { ready: true });
  }
  const result = await fetch(url, { headers: { 'X-Gregale-Dev-Session-Context': authority } });
  assert.deepEqual(await result.json(), { payments: 'remote', inventory: 'remote' });
  assert.deepEqual(observed, [['payments.svc.gregale', authority], ['inventory.svc.gregale', authority]]);
  const production = fixtureServer({ role: 'frontend', fetchImpl: async input => {
    assert.equal(new Request(input).headers.get('X-Gregale-Dev-Session-Context'), authority);
    return new Response('', { status: 403 });
  } });
  production.listen(0, '127.0.0.1');
  await once(production, 'listening');
  t.after(() => production.close());
  const denied = await fetch(`http://127.0.0.1:${production.address().port}/charge`, { headers: { 'X-Gregale-Bridge-Acceptance-Context': authority } });
  assert.equal(denied.status, 403);
  assert.deepEqual(await denied.json(), { caller: 'frontend', upstream_status: 403 });
});
