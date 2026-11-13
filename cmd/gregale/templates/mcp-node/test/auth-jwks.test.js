import test from 'node:test';
import assert from 'node:assert/strict';
import { createLocalJWKSet, exportJWK, generateKeyPair, SignJWT } from 'jose';
import { createAuth } from '../auth.js';

const config = { endpoint: '/mcp', auth: {
  mode: 'external-oauth', issuer: 'https://issuer.example', jwks_url: 'https://issuer.example/jwks',
  resource: 'https://mcp.example/mcp', scopes: ['mcp:catalog'],
} };
const { publicKey, privateKey } = await generateKeyPair('RS256', { extractable: true });
const key = { ...await exportJWK(publicKey), kid: 'provider-key' };
const credential = kid => new SignJWT({ scope: 'mcp:catalog' })
  .setProtectedHeader({ alg: 'RS256', kid }).setIssuer(config.auth.issuer)
  .setAudience(config.auth.resource).setSubject('customer').setExpirationTime('1h').sign(privateKey);

async function authenticate(auth, token) {
  let status = 401;
  const response = { setHeader() {}, status(value) { status = value; return this; }, json() { return this; } };
  await auth.middleware({ headers: { authorization: `Bearer ${token}` } }, response, () => { status = 200; });
  return status;
}

test('OAuth rejects encryption-only JWKs and accepts verification keys', async () => {
  const token = await credential(key.kid);
  for (const [operations, status] of [[['encrypt'], 401], [[], 401], [['verify'], 200]]) {
    const resolver = createLocalJWKSet({ keys: [{ ...key, key_ops: operations }] });
    assert.equal(await authenticate(createAuth(config, resolver), token), status);
  }
});

for (const failure of ['network', 'http', 'json', 'jwks']) {
  test(`failed JWKS ${failure} fetches coalesce, cool down and recover`, async t => {
    let now = 1000;
    let fetches = 0;
    let unavailable = true;
    t.mock.method(performance, 'now', () => now);
    t.mock.method(globalThis, 'fetch', async () => {
      fetches++;
      if (!unavailable) return Response.json({ keys: [key] });
      if (failure === 'network') throw new TypeError('provider unavailable');
      if (failure === 'http') return new Response('', { status: 503 });
      if (failure === 'json') return new Response('{');
      return Response.json({ keys: null });
    });
    const auth = createAuth(config);
    const token = await credential(key.kid);
    assert.deepEqual(await Promise.all(Array.from({ length: 20 }, () => authenticate(auth, token))), Array(20).fill(401));
    for (let i = 0; i < 20; i++) assert.equal(await authenticate(auth, token), 401);
    assert.equal(fetches, 1);
    unavailable = false;
    now += 29_999;
    assert.equal(await authenticate(auth, token), 401);
    assert.equal(fetches, 1);
    now++;
    assert.equal(await authenticate(auth, token), 200);
    assert.equal(fetches, 2);
    assert.equal(await authenticate(auth, token), 200);
    assert.equal(fetches, 2);
  });
}

test('failed rotation refresh does not block a still-fresh cached verification key', async t => {
  let now = Date.now();
  let fetches = 0;
  t.mock.method(Date, 'now', () => now);
  t.mock.method(performance, 'now', () => now);
  t.mock.method(globalThis, 'fetch', async () => {
    fetches++;
    return fetches === 1 ? Response.json({ keys: [key] }) : new Response('', { status: 503 });
  });
  const auth = createAuth(config);
  const known = await credential(key.kid);
  const unknown = await credential('rotated-key');
  assert.equal(await authenticate(auth, known), 200);
  now += 31_000;
  assert.equal(await authenticate(auth, unknown), 401);
  for (let i = 0; i < 5; i++) {
    assert.equal(await authenticate(auth, known), 200);
    assert.equal(await authenticate(auth, unknown), 401);
  }
  assert.equal(fetches, 2);
});

test('JWKS streams are capped at 1 MiB and oversized refreshes cool down', async t => {
  let now = 1000;
  let fetches = 0;
  let cancelled = false;
  t.mock.method(performance, 'now', () => now);
  t.mock.method(globalThis, 'fetch', async () => {
    fetches++;
    if (fetches > 1) return Response.json({ keys: [key] });
    return new Response(new ReadableStream({
      start(controller) {
        controller.enqueue(new Uint8Array(700_000).fill(32));
        controller.enqueue(new Uint8Array(400_000).fill(32));
      },
      cancel() { cancelled = true; },
    }));
  });
  const auth = createAuth(config);
  const token = await credential(key.kid);
  assert.equal(await authenticate(auth, token), 401);
  assert.equal(cancelled, true);
  for (let i = 0; i < 10; i++) assert.equal(await authenticate(auth, token), 401);
  assert.equal(fetches, 1);
  now += 30_000;
  assert.equal(await authenticate(auth, token), 200);
  assert.equal(fetches, 2);
});

test('a valid JWKS exactly at the size limit is accepted', async t => {
  const body = JSON.stringify({ keys: [key] });
  t.mock.method(globalThis, 'fetch', async () => new Response(body.padEnd(1 << 20, ' ')));
  assert.equal(await authenticate(createAuth(config), await credential(key.kid)), 200);
});
