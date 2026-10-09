import test from 'node:test';
import assert from 'node:assert/strict';

import { captureCrashSnapshot, CRASH_SNAPSHOT_ENDPOINT } from '../src/index.js';

function fakeFetch(status: number, body: unknown, calls: Array<{ url: string; init?: RequestInit }>): typeof fetch {
  return (async (url: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: String(url), init });
    return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
  }) as typeof fetch;
}

test('captureCrashSnapshot posts the request to the metadata endpoint', async () => {
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  const result = await captureCrashSnapshot({
    reason: 'checkout panic',
    route: '/orders',
    waitMs: 5000,
    fetch: fakeFetch(200, { status: 'captured', capture_id: 'c1' }, calls),
  });
  assert.deepEqual(result, { status: 'captured', captureId: 'c1', code: undefined, inFork: false });
  assert.equal(calls[0]!.url, CRASH_SNAPSHOT_ENDPOINT);
  assert.equal(calls[0]!.init?.method, 'POST');
  assert.deepEqual(JSON.parse(String(calls[0]!.init?.body)), { reason: 'checkout panic', route: '/orders', wait_ms: 5000 });
});

test('captureCrashSnapshot reports a fork and refusals without throwing', async () => {
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  assert.deepEqual(
    await captureCrashSnapshot({ fetch: fakeFetch(200, { status: 'captured', capture_id: 'c2', in_fork: true }, calls) }),
    { status: 'captured', captureId: 'c2', code: undefined, inFork: true },
  );
  assert.deepEqual(
    await captureCrashSnapshot({ fetch: fakeFetch(409, { status: 'refused' }, calls) }),
    { status: 'refused', captureId: undefined, code: undefined, inFork: false },
  );
  const failing = (async () => { throw new TypeError('fetch failed'); }) as typeof fetch;
  assert.deepEqual(await captureCrashSnapshot({ fetch: failing }), { status: 'unavailable', inFork: false });
});
