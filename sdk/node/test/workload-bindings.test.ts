import test from 'node:test';
import assert from 'node:assert/strict';
import { BINDINGS_AUDIENCE, BindingTokenSource, bindingFetch } from '../src/workload-bindings.js';

function fake(opts: { firstStatus?: number } = {}) {
  const calls: { url: string; auth: string | null }[] = [];
  let minted = 0;
  let apiCalls = 0;
  const fetchImpl = async (input: string | URL | Request, init?: RequestInit): Promise<Response> => {
    const url = new URL(String(input));
    if (url.hostname === '127.0.0.1') {
      minted += 1;
      calls.push({ url: `identity:${url.searchParams.get('audience')}`, auth: null });
      return Response.json({ access_token: `eyJ.token.${minted}`, token_type: 'Bearer', expires_in: 300 });
    }
    apiCalls += 1;
    calls.push({ url: url.pathname, auth: new Headers(init?.headers).get('authorization') });
    if (apiCalls === 1 && opts.firstStatus) return new Response('{}', { status: opts.firstStatus });
    return Response.json({ id: 'msg' }, { status: 201 });
  };
  return { calls, fetchImpl, minted: () => minted };
}

const endpoint = 'http://127.0.0.1:8181/v1/identity/token';

test('bindingFetch adds a cached gregale:bindings token and replaces API keys', async () => {
  const f = fake();
  const fetch = bindingFetch({ identityEndpoint: endpoint, fetch: f.fetchImpl });
  await fetch('https://api.gregale.dev/v1/apps/worker/queues/send', { method: 'POST', body: '{}', headers: { Authorization: 'Bearer gk_admin' } });
  await fetch('https://api.gregale.dev/v1/apps/web/delayed-tasks', { method: 'POST', body: '{}' });
  assert.equal(f.minted(), 1);
  assert.deepEqual(f.calls.map(c => c.url), [`identity:${BINDINGS_AUDIENCE}`, '/v1/apps/worker/queues/send', '/v1/apps/web/delayed-tasks']);
  assert.equal(f.calls[1].auth, 'Bearer eyJ.token.1');
  assert.equal(f.calls[2].auth, 'Bearer eyJ.token.1');
});

test('a 401 refreshes the token and retries once', async () => {
  const f = fake({ firstStatus: 401 });
  const fetch = bindingFetch({ identityEndpoint: endpoint, fetch: f.fetchImpl });
  const response = await fetch('https://api.gregale.dev/v1/apps/worker/queues/send', { method: 'POST', body: '{}' });
  assert.equal(response.status, 201);
  assert.equal(f.minted(), 2);
  assert.equal(f.calls.at(-1)?.auth, 'Bearer eyJ.token.2');
});

test('tokens refresh before expiry', async () => {
  const f = fake();
  let now = 0;
  const source = new BindingTokenSource({ identityEndpoint: endpoint, fetch: f.fetchImpl, now: () => now });
  assert.equal(await source.get(), 'eyJ.token.1');
  now = 200_000;
  assert.equal(await source.get(), 'eyJ.token.1');
  now = 241_000;
  assert.equal(await source.get(), 'eyJ.token.2');
});

test('only loopback endpoints are accepted', () => {
  assert.throws(() => new BindingTokenSource({ identityEndpoint: 'https://169.254.169.254/token' }), /loopback/);
  assert.throws(() => new BindingTokenSource({ identityEndpoint: '' }), /FAAS_WORKLOAD_IDENTITY_ENDPOINT/);
});
