import test from 'node:test';
import assert from 'node:assert/strict';

import { createFunctions, FunctionCallError, functionBaseURL, functionsFromEnv } from '../src/functions-runtime.js';

type Recorded = { url: string; init: RequestInit };

function fakeFetch(responses: Response[]): { fetch: typeof globalThis.fetch; calls: Recorded[] } {
  const calls: Recorded[] = [];
  return {
    calls,
    fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init: init ?? {} });
      return responses.shift() ?? new Response('', { status: 500 });
    }) as typeof globalThis.fetch,
  };
}

test('functionsFromEnv reads the deploy map and ignores bad values', () => {
  assert.deepEqual(functionsFromEnv({ GREGALE_FUNCTIONS: '{"users":"shop-users","x":1}' }), { users: 'shop-users' });
  assert.deepEqual(functionsFromEnv({ GREGALE_FUNCTIONS: 'not json' }), {});
  assert.deepEqual(functionsFromEnv({}), {});
});

test('functionBaseURL prefers the injected binding URL', () => {
  assert.equal(functionBaseURL('shop-users', {}), 'http://shop-users.internal:10081');
  assert.equal(functionBaseURL('shop-users', { GREGALE_SERVICE_SHOP_USERS_URL: 'https://shop-users.internal/' }), 'https://shop-users.internal');
});

test('call posts JSON over the binding and parses the output', async () => {
  const { fetch, calls } = fakeFetch([Response.json({ id: '42', name: 'Ada' })]);
  type Fns = { users: { input: { id: string }; output: { id: string; name: string } } };
  const fns = createFunctions<Fns>({ functions: { users: 'shop-users' }, env: {}, fetch });
  const user = await fns.users.call({ id: '42' });
  assert.equal(user.name, 'Ada');
  const [first] = calls;
  assert.equal(first?.url, 'http://shop-users.internal:10081/');
  assert.equal(first?.init.method, 'POST');
  assert.equal(first?.init.body, '{"id":"42"}');
});

test('send asks for an async invocation and returns its id', async () => {
  const { fetch, calls } = fakeFetch([Response.json({ invocation_id: 'inv-1' }, { status: 202 })]);
  type Fns = { mailer: { input: { to: string }; output: unknown } };
  const fns = createFunctions<Fns>({ functions: { mailer: 'shop-mailer' }, env: {}, fetch });
  assert.equal((await fns.mailer.send({ to: 'a@example.com' })).invocationId, 'inv-1');
  assert.equal(new Headers(calls[0]?.init.headers).get('x-gregale-invoke'), 'async');
  assert.equal(new Headers(calls[0]?.init.headers).get('x-gregale-function-call'), '1');
});

test('a non-2xx answer raises FunctionCallError with the problem', async () => {
  const problem = { code: 'function_input_invalid', detail: '/id: required', status: 422 };
  const { fetch } = fakeFetch([Response.json(problem, { status: 422 })]);
  type Fns = { users: { input: Record<string, unknown>; output: unknown } };
  const fns = createFunctions<Fns>({ functions: { users: 'shop-users' }, env: {}, fetch });
  await assert.rejects(fns.users.call({}), (err: unknown) => {
    if (!(err instanceof FunctionCallError)) return false;
    assert.equal(err.status, 422);
    assert.equal(err.code, 'function_input_invalid');
    return true;
  });
});

test('an uncallable name says what is callable', () => {
  const fns = createFunctions({ functions: { users: 'shop-users' }, env: {} });
  assert.throws(() => fns.billing, /not callable from here; callable: users/);
});

test('send names the completion function and wait long-polls the sent invocation', async () => {
  const { fetch, calls } = fakeFetch([
    Response.json({ id: 'inv-2' }, { status: 202 }),
    Response.json({ id: 'inv-2', state: 'dispatching', done: false, attempts: 1 }),
    Response.json({ id: 'inv-2', state: 'completed', done: true, attempts: 1, result: { sent: true } }),
  ]);
  type Fns = { mailer: { input: { to: string }; output: { sent: boolean } } };
  const fns = createFunctions<Fns>({ functions: { mailer: 'shop-mailer' }, env: {}, fetch });
  const invocation = await fns.mailer.send({ to: 'a@example.com' }, { onComplete: 'self' });
  assert.equal(new Headers(calls[0]?.init.headers).get('x-gregale-on-complete'), 'self');
  const result = await invocation.wait({ timeoutMs: 5_000 });
  assert.deepEqual(result, { sent: true });
  assert.match(calls[1]?.url ?? '', /^http:\/\/api\.svc\.gregale:10081\/v1\/invocations\/inv-2\?wait=\d+$/);
});

test('wait raises FunctionCallError for a failed invocation', async () => {
  const { fetch } = fakeFetch([
    Response.json({ id: 'inv-3' }, { status: 202 }),
    Response.json({ id: 'inv-3', state: 'failed', done: true, attempts: 3, error: 'boom' }),
  ]);
  type Fns = { mailer: { input: unknown; output: unknown } };
  const fns = createFunctions<Fns>({ functions: { mailer: 'shop-mailer' }, env: {}, fetch });
  const invocation = await fns.mailer.send({});
  await assert.rejects(invocation.wait(), (err: unknown) => err instanceof FunctionCallError && err.code === 'function_invocation_failed');
});

// ADR-20261113120824 §3: start() posts to the platform API and returns a run
// handle whose wait long-polls the run and signal sends it an event.
test('start returns a workflow run handle', async () => {
  const { fetch, calls } = fakeFetch([
    Response.json({ id: 'run-1', status: 'pending', done: false }, { status: 201 }),
    new Response(null, { status: 202 }),
    Response.json({ id: 'run-1', status: 'succeeded', done: true, output: { shipped: true } }),
    Response.json({ error: 'nope' }, { status: 201 }),
  ]);
  type Fns = { orders: { input: { id: number }; output: { shipped: boolean } } };
  const fns = createFunctions<Fns>({ functions: { orders: 'shop-orders' }, env: {}, fetch });
  const run = await fns.orders.start({ id: 1 }, { idempotencyKey: 'order-1' });
  assert.equal(run.runId, 'run-1');
  assert.equal(calls[0]?.url, 'http://api.svc.gregale:10081/v1/workflow-runs');
  assert.equal(new Headers(calls[0]?.init.headers).get('idempotency-key'), 'order-1');
  assert.deepEqual(JSON.parse(String(calls[0]?.init.body)), { function: 'orders', input: { id: 1 } });
  await run.signal('approved', { ok: true });
  assert.equal(calls[1]?.url, 'http://api.svc.gregale:10081/v1/workflow-runs/run-1/events');
  assert.deepEqual(await run.wait({ timeoutMs: 5_000 }), { shipped: true });
  assert.match(calls[2]?.url ?? '', /\/v1\/workflow-runs\/run-1\?wait=\d+$/);
  await assert.rejects(fns.orders.start({ id: 2 }), FunctionCallError);
});
