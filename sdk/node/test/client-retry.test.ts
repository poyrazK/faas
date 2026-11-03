import test, { type TestContext } from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate as nextTurn } from 'node:timers/promises';

import { FaaSClient, type FaaSClientOptions } from '../src/client.js';
import { ErrCapacity, isFaasError } from '../src/errors.js';
import { AppsService } from '../src/generated/services/AppsService.js';
import { OpenAPI } from '../src/generated/core/OpenAPI.js';
import { ApiError } from '../src/generated/core/ApiError.js';

const endpoint = 'https://api.example.test/v1/apps';

function clientFixture(t: TestContext, fetch: typeof globalThis.fetch, options: FaaSClientOptions = {}): FaaSClient {
  const originalFetch = globalThis.fetch;
  const originalConfig = { ...OpenAPI };
  const client = new FaaSClient('https://api.example.test', {
    retry: { maxAttempts: 2, backoffMs: 0 }, fetch, ...options,
  });
  t.after(() => {
    client.uninstall();
    globalThis.fetch = originalFetch;
    Object.assign(OpenAPI, originalConfig);
  });
  return client;
}

function unavailable(txId = 'fixture', retryAfter?: string): Response {
  return Response.json({ status: 503, code: 'capacity_unavailable', detail: 'Try later', tx_id: txId }, {
    status: 503,
    headers: { 'Content-Type': 'application/problem+json', ...(retryAfter === undefined ? {} : { 'Retry-After': retryAfter }) },
  });
}

test('a retry after an accepted write reuses its key and body', async t => {
  const accepted = new Map<string, { id: string; slug: string }>();
  const keys: string[] = [];
  const bodies: unknown[] = [];
  clientFixture(t, async (_input, init) => {
    const key = new Headers(init?.headers).get('Idempotency-Key');
    assert.ok(key);
    keys.push(key);
    bodies.push(init?.body);
    if (!accepted.has(key)) accepted.set(key, { id: String(accepted.size + 1), slug: 'report' });
    // Model an accepted mutation whose first acknowledgement fails.
    if (keys.length === 1) return new Response('Unavailable', { status: 503 });
    return Response.json(accepted.get(key));
  });
  const result = await AppsService.createApp({ requestBody: { slug: 'report' } });
  assert.equal(result.id, '1');
  assert.equal(accepted.size, 1);
  assert.equal(keys.length, 2);
  assert.equal(new Set(keys).size, 1);
  assert.deepEqual(bodies, [JSON.stringify({ slug: 'report' }), JSON.stringify({ slug: 'report' })]);
});

for (const status of [429, 503]) {
  for (const contentType of ['application/json', 'application/problem+json']) {
    test('configured retries handle ' + status + ' with ' + contentType, async t => {
      let attempts = 0;
      clientFixture(t, async () => {
        if (++attempts === 1) return Response.json({ status, code: status === 429 ? 'rate_limited' : 'capacity_unavailable' }, {
          status, headers: { 'Content-Type': contentType, 'Retry-After': '0' },
        });
        return Response.json({ id: 'app', slug: 'report' });
      });
      assert.equal((await AppsService.createApp({ requestBody: { slug: 'report' } })).id, 'app');
      assert.equal(attempts, 2);
    });
  }
}

test('exhaustion preserves the final typed problem and retry metadata', async t => {
  let attempts = 0;
  clientFixture(t, async () => unavailable('attempt-' + ++attempts, '0'));
  await assert.rejects(AppsService.createApp({ requestBody: { slug: 'report' } }), error => {
    assert.ok(error instanceof ErrCapacity);
    assert.ok(isFaasError(error));
    assert.equal(error.status, 503);
    assert.equal(error.txId, 'attempt-2');
    assert.equal(error.problem.detail, 'Try later');
    assert.equal(error.retryAfter, '0');
    return true;
  });
  assert.equal(attempts, 2);
});

test('exhaustion leaves a plain-text final error readable by the generated client', async t => {
  let attempts = 0;
  clientFixture(t, async () => new Response('failure-' + ++attempts, {
    status: 503, headers: { 'Content-Type': 'text/plain' },
  }));
  await assert.rejects(AppsService.createApp({ requestBody: { slug: 'report' } }), error => {
    assert.ok(error instanceof ApiError);
    assert.equal(error.status, 503);
    assert.equal(error.body, 'failure-2');
    return true;
  });
  assert.equal(attempts, 2);
});

for (const status of [400, 401, 403, 404, 409, 422]) {
  test('non-retryable HTTP ' + status + ' fails on its first attempt', async t => {
    let attempts = 0;
    clientFixture(t, async () => {
      attempts++;
      return Response.json({ status, detail: 'Rejected' }, { status });
    });
    await assert.rejects(AppsService.createApp({ requestBody: { slug: 'report' } }), error => isFaasError(error) && error.status === status);
    assert.equal(attempts, 1);
  });
}

// A traffic policy check that ran out of time saved nothing and asks to be
// resent (422 traffic_policy_too_complex with Retry-After). A 422 without
// Retry-After, or with another code, is a verdict and fails at once.
for (const { name, code, retryAfter, attempts: want } of [
  { name: 'is resent', code: 'traffic_policy_too_complex', retryAfter: '0', attempts: 2 },
  { name: 'without Retry-After fails at once', code: 'traffic_policy_too_complex', retryAfter: undefined, attempts: 1 },
  { name: 'with another code fails at once', code: 'validation_error', retryAfter: '0', attempts: 1 },
]) {
  test('a timed-out policy check ' + name, async t => {
    let attempts = 0;
    const bodies: unknown[] = [];
    clientFixture(t, async (_input, init) => {
      bodies.push(init?.body);
      if (++attempts === 1) return Response.json({ status: 422, code, detail: 'Policy check ran out of time' }, {
        status: 422, headers: { 'Content-Type': 'application/problem+json', ...(retryAfter === undefined ? {} : { 'Retry-After': retryAfter }) },
      });
      return Response.json({ id: 'app', slug: 'report' });
    });
    const result = AppsService.createApp({ requestBody: { slug: 'report' } });
    if (want === 2) assert.equal((await result).id, 'app');
    else await assert.rejects(result, error => isFaasError(error) && error.status === 422 && error.problem.code === code);
    assert.equal(attempts, want);
    assert.equal(new Set(bodies).size, 1);
  });
}

test('the default policy still makes one attempt', async t => {
  let attempts = 0;
  clientFixture(t, async () => { attempts++; return unavailable(); }, { retry: undefined });
  await assert.rejects(AppsService.createApp({ requestBody: { slug: 'report' } }), ErrCapacity);
  assert.equal(attempts, 1);
});

test('concurrent logical writes have distinct keys that stay stable on retry', async t => {
  const keys = new Map<string, string[]>();
  clientFixture(t, async (_input, init) => {
    const slug = JSON.parse(String(init?.body)).slug as string;
    const key = new Headers(init?.headers).get('Idempotency-Key');
    assert.ok(key);
    const attempts = keys.get(slug) ?? [];
    keys.set(slug, [...attempts, key]);
    if (attempts.length === 0) return unavailable();
    return Response.json({ id: slug, slug });
  });
  await Promise.all(['api', 'worker'].map(slug => AppsService.createApp({ requestBody: { slug } })));
  assert.equal(keys.size, 2);
  for (const attempts of keys.values()) {
    assert.equal(attempts.length, 2);
    assert.equal(new Set(attempts).size, 1);
  }
  assert.notEqual(keys.get('api')![0], keys.get('worker')![0]);
});

test('an explicit request key overrides the client key without changing caller headers', async t => {
  const keys: Array<string | null> = [];
  const client = clientFixture(t, async (_input, init) => {
    keys.push(new Headers(init?.headers).get('Idempotency-Key'));
    return keys.length === 1 ? unavailable() : Response.json({ ok: true });
  });
  client.setIdempotencyKey('client-key');
  const headers = new Headers({ 'Idempotency-Key': 'request-key' });
  await fetch(endpoint, { method: 'POST', headers, body: '{}' });
  assert.deepEqual(keys, ['request-key', 'request-key']);
  assert.equal(headers.get('Idempotency-Key'), 'request-key');
});

for (const method of ['GET', 'HEAD']) {
  test(method + ' Request inputs preserve headers without minting write keys', async t => {
    let attempts = 0;
    clientFixture(t, async (_input, init) => {
      const headers = new Headers(init?.headers);
      assert.equal(headers.get('X-Fixture'), 'read');
      assert.equal(headers.get('Idempotency-Key'), null);
      assert.equal(init?.method, method);
      return ++attempts === 1 ? new Response(null, { status: 503 }) : new Response(null, { status: 200 });
    });
    assert.equal((await fetch(new Request(endpoint, { method, headers: { 'X-Fixture': 'read' } }))).status, 200);
    assert.equal(attempts, 2);
  });
}

test('one-shot Request bodies are sent once even when retries are configured', async t => {
  let attempts = 0;
  clientFixture(t, async (input, init) => {
    attempts++;
    const request = new Request(input, init);
    assert.equal(request.method, 'POST');
    assert.ok(request.headers.get('Idempotency-Key'));
    assert.equal(await request.text(), 'one-shot');
    return unavailable();
  });
  await assert.rejects(fetch(new Request(endpoint, { method: 'POST', body: 'one-shot' })), ErrCapacity);
  assert.equal(attempts, 1);
});

test('one-shot ReadableStream uploads are not replayed', async t => {
  let attempts = 0;
  clientFixture(t, async (input, init) => {
    attempts++;
    assert.equal(await new Request(input, init).text(), 'stream');
    return unavailable();
  });
  const init = {
    method: 'POST', duplex: 'half',
    body: new ReadableStream({ start(controller) { controller.enqueue(new TextEncoder().encode('stream')); controller.close(); } }),
  };
  await assert.rejects(fetch(endpoint, init), ErrCapacity);
  assert.equal(attempts, 1);
});

for (const header of ['seconds', 'date', 'invalid']) {
  test('retry delay honors ' + header + ' Retry-After and policy backoff', async t => {
    const now = Date.UTC(2026, 9, 14);
    t.mock.timers.enable({ apis: ['setTimeout', 'Date'], now });
    const retryAfter = header === 'seconds' ? '2' : header === 'date' ? new Date(now + 2000).toUTCString() : 'invalid';
    const expectedDelay = header === 'invalid' ? 100 : 2000;
    const timestamps: number[] = [];
    clientFixture(t, async () => {
      timestamps.push(Date.now());
      return timestamps.length === 1 ? unavailable('first', retryAfter) : Response.json({ ok: true });
    }, { retry: { maxAttempts: 2, backoffMs: 100 } });
    const pending = fetch(endpoint);
    void pending.catch(() => undefined);
    await nextTurn();
    assert.equal(timestamps.length, 1);
    t.mock.timers.tick(expectedDelay - 1);
    await nextTurn();
    assert.equal(timestamps.length, 1);
    t.mock.timers.tick(1);
    await pending;
    assert.deepEqual(timestamps, [now, now + expectedDelay]);
  });
}

test('cancellation interrupts Retry-After waiting without another attempt', async t => {
  const controller = new AbortController();
  const reason = new Error('Cancelled');
  let attempts = 0;
  clientFixture(t, async () => { attempts++; return unavailable('first', '3600'); });
  const rejected = assert.rejects(fetch(endpoint, { signal: controller.signal }), error => error === reason);
  await nextTurn();
  controller.abort(reason);
  await rejected;
  assert.equal(attempts, 1);
});

test('an already cancelled Request sends no attempt', async t => {
  const reason = new Error('Already cancelled');
  let attempts = 0;
  clientFixture(t, async () => { attempts++; return Response.json({}); });
  await assert.rejects(fetch(new Request(endpoint, { signal: AbortSignal.abort(reason) })), error => error === reason);
  assert.equal(attempts, 0);
});

test('the configured timeout includes Retry-After waiting', async t => {
  let attempts = 0;
  clientFixture(t, async () => { attempts++; return unavailable('first', '3600'); }, { timeoutMs: 10 });
  await assert.rejects(fetch(endpoint), error => error instanceof DOMException && error.name === 'TimeoutError');
  assert.equal(attempts, 1);
});

test('cancellation before final error decoding preserves the caller reason', async t => {
  const controller = new AbortController();
  const reason = new Error('Cancelled after response');
  clientFixture(t, async () => { controller.abort(reason); return unavailable(); }, {
    retry: { maxAttempts: 1, backoffMs: 0 },
  });
  await assert.rejects(fetch(endpoint, { signal: controller.signal }), error => error === reason);
});

test('an aborted Problem body read is not converted into a capacity error', async t => {
  const controller = new AbortController();
  const reason = new Error('Cancelled while reading');
  clientFixture(t, async () => {
    const response = unavailable();
    t.mock.method(response, 'json', async () => { controller.abort(reason); throw reason; });
    return response;
  }, { retry: { maxAttempts: 1, backoffMs: 0 } });
  await assert.rejects(fetch(endpoint, { signal: controller.signal }), error => error === reason);
});

test('transport failures remain caller-owned and do not retry a write', async t => {
  const reason = new Error('Connection lost');
  let attempts = 0;
  clientFixture(t, async () => { attempts++; throw reason; });
  await assert.rejects(AppsService.createApp({ requestBody: { slug: 'report' } }), error => error === reason);
  assert.equal(attempts, 1);
});
