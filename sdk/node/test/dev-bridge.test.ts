import assert from 'node:assert/strict';
import test from 'node:test';
import { createDevBridgeFetch, currentDevBridgeContext, devBridgeMiddleware, DEV_BRIDGE_CONTEXT_HEADER, withDevBridgeContext } from '../src/dev-bridge.js';

const alice = `account.${Buffer.alloc(32, 1).toString('base64url')}.${Buffer.alloc(32, 2).toString('base64url')}`;
const bob = `account.${Buffer.alloc(32, 3).toString('base64url')}.${Buffer.alloc(32, 4).toString('base64url')}`;

test('concurrent developer requests and unscoped handlers remain isolated', async () => {
  const observed: string[] = [];
  const fetch = createDevBridgeFetch(async input => {
    const request = new Request(input);
    observed.push(request.headers.get(DEV_BRIDGE_CONTEXT_HEADER) ?? 'ordinary');
    assert.equal(request.headers.get('X-Gregale-Dev-Bridge-Token'), null);
    return new Response('ok');
  });
  await Promise.all([alice, bob, undefined].map(value => withDevBridgeContext(value, async () => {
    await new Promise(resolve => setImmediate(resolve));
    await fetch('http://payments.svc.gregale', { headers: { 'X-Gregale-Dev-Bridge-Token': 'forged' } });
  })));
  assert.deepEqual(observed.sort(), [alice, bob, 'ordinary'].sort());
  assert.equal(currentDevBridgeContext(), undefined);
});

test('external destinations, nested hosts and explicit credentials cannot inherit authority', async () => {
  const fetch = createDevBridgeFetch(async input => {
    const request = new Request(input);
    assert.equal(request.headers.get(DEV_BRIDGE_CONTEXT_HEADER), null);
    assert.equal(request.headers.get('X-Gregale-Dev-Bridge-Account'), null);
    assert.equal(request.headers.get('Authorization'), 'Bearer application-auth');
    return new Response('ok');
  });
  await withDevBridgeContext(alice, async () => {
    for (const host of ['stripe.example', 'nested.payments.svc.gregale', 'payments.svc.gregale.attacker.example']) {
      await fetch(`https://${host}`, { headers: { [DEV_BRIDGE_CONTEXT_HEADER]: alice, 'X-Gregale-Dev-Bridge-Account': 'account', Authorization: 'Bearer application-auth' } });
    }
  });
});

test('scoped fetch uses manual redirects and Express middleware rejects duplicate contexts', async () => {
  const fetch = createDevBridgeFetch(async input => {
    const request = new Request(input);
    assert.equal(request.redirect, 'manual');
    assert.equal(request.headers.get(DEV_BRIDGE_CONTEXT_HEADER), alice);
    return new Response(null, { status: 302, headers: { Location: 'https://external.example' } });
  });
  const response = await withDevBridgeContext(alice, () => fetch('http://payments.internal'));
  assert.equal(response.status, 302);
  devBridgeMiddleware({ headers: { [DEV_BRIDGE_CONTEXT_HEADER.toLowerCase()]: [alice, bob] } }, {}, () => {
    assert.equal(currentDevBridgeContext(), undefined);
  });
  devBridgeMiddleware({ headers: { [DEV_BRIDGE_CONTEXT_HEADER.toLowerCase()]: alice } }, {}, () => {
    assert.equal(currentDevBridgeContext(), alice);
  });
});
