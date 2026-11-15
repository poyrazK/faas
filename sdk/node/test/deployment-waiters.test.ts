import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once, getEventListeners } from 'node:events';
import { setImmediate as nextTurn } from 'node:timers/promises';
import {
  createClient, OpenAPI, WaiterError, ErrNotFound, ErrUnauthorized,
  type BuildResponse, type ReleaseTimeline, type BuildWaitOptions, type DeploymentWaiters,
  type WaiterSnapshot, type WaiterProgress, type CancelablePromise, type ResponseInfo,
} from '../src/index.js';
import { createClient as createPreviewClient } from '../src/preview.js';

const build = (id = 'build/1', status: BuildResponse['status'] = 'succeeded'): BuildResponse => ({
  id, status, deployment_id: 'deployment', kind: 'tarball', source_bytes: 100, enqueued_at: '2026-11-13T21:00:00Z',
});
const release = (id = 'deployment/1', phase: ReleaseTimeline['phase'] = 'released'): ReleaseTimeline => ({
  deployment_id: id, phase, status: ['building', 'failed', 'cancelled', 'superseded'].includes(phase) ? phase : 'live', traffic_percent: 100,
  app: 'api', summary: phase, created_at: '2026-11-13T21:00:00Z', events: [],
});
const fast = { timeoutMs: 2000, minDelayMs: 1, maxDelayMs: 1 };
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>(done => { resolve = done; });
  return { promise, resolve };
}

for (const [surface, factory] of [['public', createClient], ['preview', createPreviewClient]] as const) {
  test(`${surface} build waiter polls by ID and reports frozen progress before typed success`, async () => {
    const states: BuildResponse['status'][] = ['queued', 'running', 'succeeded'];
    const snapshots: WaiterSnapshot<BuildResponse>[] = [], progress: WaiterProgress[] = [];
    let calls = 0;
    const client = factory('https://build.test/prefix', { fetch: async (url, init) => {
      assert.equal(String(url), 'https://build.test/prefix/v1/builds/build%2F1');
      assert.equal(init?.method, 'GET');
      return Response.json(build('build/1', states[calls++]!));
    } });
    const waitForBuild = client.deployments.waitForBuild;
    const result = await waitForBuild({ id: 'build/1' }, { ...fast, onProgress: (value, info) => {
      snapshots.push(value); progress.push(info);
      assert.ok(Object.isFrozen(value)); assert.ok(Object.isFrozen(info));
      assert.equal(Reflect.set(value, 'status', 'succeeded'), false);
    } });
    assert.equal(result.status, 'succeeded'); assert.equal(calls, 3);
    assert.deepEqual(snapshots.map(value => value.status), states);
    assert.deepEqual(progress.map(info => info.poll), [1, 2, 3]);
    assert.ok(progress.every(info => Number.isFinite(info.elapsedMs) && info.elapsedMs >= 0));
    assert.notEqual(result, snapshots[2]); assert.equal(Object.isFrozen(result), false);
  });

  test(`${surface} deployment waiter follows authoritative release phases, including watch completion`, async () => {
    const phases: ReleaseTimeline['phase'][] = ['building', 'scheduled', 'awaiting_approval', 'held', 'rolling_out', 'watching', 'released'];
    const seen: string[] = [];
    let calls = 0;
    const client = factory('https://releases.test/prefix', { fetch: async (url, init) => {
      assert.equal(String(url), 'https://releases.test/prefix/v1/deployments/deployment%2F1/timeline');
      assert.equal(init?.method, 'GET');
      return Response.json(release('deployment/1', phases[calls++]!));
    } });
    const result = await client.deployments.waitForDeployment({ id: 'deployment/1' }, { ...fast, stopOnBlocked: false, onProgress: value => { seen.push(value.phase); } });
    assert.deepEqual(seen, phases); assert.equal(result.phase, 'released'); assert.equal(calls, 7);
  });
}

for (const status of ['failed', 'cancelled'] as const) {
  test(`build ${status} is a typed terminal failure with its last valid observation`, async () => {
    const row = { ...build('build/1', status), failure_class: 'oom' as const };
    const client = createClient('https://failure.test', { fetch: async () => Response.json(row) });
    await assert.rejects(client.deployments.waitForBuild({ id: row.id }, fast), error => {
      assert.ok(error instanceof WaiterError);
      assert.equal(error.code, status); assert.equal(error.resource, 'build'); assert.equal(error.id, row.id); assert.equal(error.polls, 1);
      assert.deepEqual(error.lastObserved, row); assert.ok(Object.isFrozen(error.lastObserved));
      return true;
    });
  });
}

for (const phase of ['failed', 'cancelled', 'rolled_back', 'superseded', 'awaiting_approval', 'held'] as const) {
  test(`deployment ${phase} stops with an inspectable outcome and can be resumed by ID`, async () => {
    let calls = 0;
    const client = createClient('https://terminal.test', { fetch: async () => Response.json(release('deployment/1', ++calls === 1 ? phase : 'released')) });
    await assert.rejects(client.deployments.waitForDeployment({ id: 'deployment/1' }, fast), error => {
      assert.ok(error instanceof WaiterError);
      assert.equal(error.code, phase === 'held' || phase === 'awaiting_approval' ? 'blocked' : phase);
      assert.equal(error.lastObserved?.phase, phase); assert.equal(error.polls, 1);
      return true;
    });
    assert.equal((await client.deployments.waitForDeployment({ id: 'deployment/1' }, fast)).phase, 'released');
    assert.equal(calls, 2);
  });
}

test('a released deployment must serve traffic and must not retain a watch regression', async () => {
  for (const [row, code] of [
    [{ ...release(), traffic_percent: 0 }, 'not_serving'],
    [{ ...release(), status: 'superseded' }, 'not_serving'],
    [{ ...release(), events: [{ at: 'now', kind: 'watch_regressed', summary: 'automatic rollback could not complete' }] }, 'regressed'],
  ] as const) {
    const client = createClient('https://released.test', { fetch: async () => Response.json(row) });
    await assert.rejects(client.deployments.waitForDeployment({ id: 'deployment/1' }, fast), error => error instanceof WaiterError && error.code === code);
  }
});

test('progress snapshots are deeply frozen, and callback failures retain their identity', async () => {
  const row = { ...release(), events: [{ at: 'now', kind: 'note' as const, summary: 'original' }] };
  const client = createClient('https://progress.test', { fetch: async () => Response.json(row) });
  const result = await client.deployments.waitForDeployment({ id: 'deployment/1' }, { ...fast, onProgress: value => {
    assert.ok(Object.isFrozen(value.events)); assert.ok(Object.isFrozen(value.events[0]));
    assert.equal(Reflect.set(value.events[0]!, 'summary', 'changed'), false);
  } });
  assert.equal(result.events[0]!.summary, 'original');
  const reason = new Error('progress consumer failed');
  await assert.rejects(client.deployments.waitForDeployment({ id: 'deployment/1' }, { ...fast, onProgress: async () => { throw reason; } }), error => error === reason);
});

test('an unfinished progress callback is bounded by the total deadline', { timeout: 5000 }, async () => {
  let calls = 0;
  const client = createClient('https://callback.test', { fetch: async () => { calls++; return Response.json(build('build/1', 'running')); } });
  await assert.rejects(client.deployments.waitForBuild({ id: 'build/1' }, { timeoutMs: 20, onProgress: () => new Promise<void>(() => {}) }), error => {
    assert.ok(error instanceof WaiterError); assert.equal(error.code, 'timeout'); assert.equal(error.name, 'TimeoutError');
    assert.equal(error.lastObserved?.status, 'running'); assert.equal(error.polls, 1); return true;
  });
  assert.equal(calls, 1);
});

test('aborting during asynchronous progress prevents later reads when the callback resumes', async () => {
  const entered = deferred(), finish = deferred();
  const controller = new AbortController(), reason = new Error('stop callback');
  let calls = 0;
  const client = createClient('https://callback-abort.test', { fetch: async () => { calls++; return Response.json(build('build/1', 'queued')); } });
  const pending = client.deployments.waitForBuild({ id: 'build/1' }, { ...fast, signal: controller.signal, onProgress: async () => { entered.resolve(); await finish.promise; } });
  const rejected = assert.rejects(pending, error => error === reason);
  await entered.promise; controller.abort(reason); await rejected;
  finish.resolve(); await nextTurn();
  assert.equal(calls, 1); assert.equal(getEventListeners(controller.signal, 'abort').length, 0);
});

test('invalid budgets and limits fail before HTTP', async () => {
  let calls = 0;
  const client = createClient('https://options.test', { fetch: async () => { calls++; return Response.json(build()); } });
  for (const timeoutMs of [0, -1, 0.5, NaN, Infinity, 2 ** 31]) await assert.rejects(client.deployments.waitForBuild({ id: 'build/1' }, { timeoutMs }), TypeError);
  for (const maxPolls of [0, -1, 0.5, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1]) await assert.rejects(client.deployments.waitForBuild({ id: 'build/1' }, { ...fast, maxPolls }), TypeError);
  for (const minDelayMs of [0, -1, 0.5, Infinity, 2 ** 31, 5001]) await assert.rejects(client.deployments.waitForBuild({ id: 'build/1' }, { timeoutMs: 1000, minDelayMs }), TypeError);
  for (const maxDelayMs of [0, -1, 0.5, Infinity, 2 ** 31]) await assert.rejects(client.deployments.waitForBuild({ id: 'build/1' }, { ...fast, maxDelayMs }), TypeError);
  await assert.rejects(client.deployments.waitForBuild({ id: '' }, fast), TypeError);
  assert.equal(calls, 0);
});

test('invalid responses and unknown states preserve the previous valid observation', async () => {
  for (const row of [null, {}, { ...build(), id: 'another-build' }, { ...build(), status: 'future_state' }]) {
    let calls = 0, progress = 0;
    const client = createClient('https://invalid.test', { fetch: async () => Response.json(++calls === 1 ? build('build/1', 'queued') : row) });
    await assert.rejects(client.deployments.waitForBuild({ id: 'build/1' }, { ...fast, onProgress: () => { progress++; } }), error => {
      assert.ok(error instanceof WaiterError); assert.equal(error.code, row && 'status' in row && row.status === 'future_state' ? 'unexpected_state' : 'invalid_response');
      assert.equal(error.lastObserved?.status, 'queued'); assert.equal(error.polls, 2); return true;
    });
    assert.equal(progress, 1);
  }
  for (const row of [{ ...release(), deployment_id: 'other' }, { ...release(), phase: 'unknown' }, { ...release(), traffic_percent: -1 }, { ...release(), events: [null] }]) {
    const client = createClient('https://invalid-release.test', { fetch: async () => Response.json(row) });
    await assert.rejects(client.deployments.waitForDeployment({ id: 'deployment/1' }, fast), error => error instanceof WaiterError && error.lastObserved === undefined);
  }
});

test('poll limits count status reads while HTTP retries retain per-poll diagnostics', async () => {
  const reports: ResponseInfo[] = [];
  let calls = 0;
  const options: BuildWaitOptions = { ...fast, maxPolls: 2, retry: { maxAttempts: 2, backoffMs: 1 }, onResponse: info => { reports.push(info); } };
  const client = createClient('https://poll-limit.test', { fetch: async () => ++calls === 1 ? new Response('{}', { status: 503 }) : Response.json(build('build/1', 'running')) });
  const pending = client.deployments.waitForBuild({ id: 'build/1' }, options);
  (options.retry as { maxAttempts: number }).maxAttempts = 1;
  await assert.rejects(pending, error => error instanceof WaiterError && error.code === 'poll_limit' && error.polls === 2 && error.lastObserved?.status === 'running');
  assert.equal(calls, 3); assert.deepEqual(reports.map(info => info.attempt), [1, 2, 1]);
});

test('polling uses bounded exponential jitter and releases its delay timer on abort', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  t.mock.method(Math, 'random', () => 0.999);
  const controller = new AbortController(), reason = new Error('stop backoff');
  let calls = 0;
  const client = createClient('https://jitter.test', { fetch: async () => { calls++; return Response.json(build('build/1', 'queued')); } });
  const rejected = assert.rejects(client.deployments.waitForBuild({ id: 'build/1' }, { timeoutMs: 1000, minDelayMs: 10, maxDelayMs: 25, signal: controller.signal }), error => error === reason);
  await nextTurn(); assert.equal(calls, 1);
  for (const delay of [10, 20, 25, 25]) {
    const before: number = calls;
    t.mock.timers.tick(delay - 1); await nextTurn(); assert.equal(calls, before);
    t.mock.timers.tick(1); await nextTurn(); assert.equal(calls, before + 1);
  }
  controller.abort(reason); await rejected;
  t.mock.timers.tick(1000); await nextTurn(); assert.equal(calls, 5);
  assert.equal(getEventListeners(controller.signal, 'abort').length, 0);
});

test('pre-abort and abort during headers keep the caller reason and affect only that wait', async () => {
  const started = deferred(), controller = new AbortController(), reason = new Error('stop headers');
  let calls = 0;
  const client = createClient('https://abort.test', { fetch: async () => { calls++; started.resolve(); return new Promise<Response>(() => {}); } });
  await assert.rejects(client.deployments.waitForBuild({ id: 'build/1' }, { ...fast, signal: AbortSignal.abort(reason) }), error => error === reason);
  assert.equal(calls, 0);
  const rejected = assert.rejects(client.deployments.waitForBuild({ id: 'build/1' }, { ...fast, signal: controller.signal }), error => error === reason);
  await started.promise; controller.abort(reason); await rejected;
  assert.equal(calls, 1); assert.equal(getEventListeners(controller.signal, 'abort').length, 0);
});

for (const action of ['timeout', 'abort'] as const) {
  test(`${action} cancels a blocked custom response body`, { timeout: 5000 }, async t => {
    t.mock.method(console, 'error', () => {});
    let cancelled = 0;
    const received = deferred(), controller = new AbortController(), reason = new Error('stop body');
    const client = createClient('https://body.test', { onResponse: () => { received.resolve(); }, fetch: async () => new Response(new ReadableStream({
      start(stream) { stream.enqueue(new TextEncoder().encode('{"id":')); }, cancel() { cancelled++; },
    }), { headers: { 'Content-Type': 'application/json' } }) });
    const pending = client.deployments.waitForBuild({ id: 'build/1' }, { timeoutMs: action === 'timeout' ? 20 : 2000, signal: controller.signal });
    const rejected = assert.rejects(pending, error => action === 'timeout' ? error instanceof WaiterError && error.code === 'timeout' : error === reason);
    await received.promise; if (action === 'abort') controller.abort(reason);
    await rejected; await nextTurn(); assert.equal(cancelled, 1);
  });
}

test('one deadline covers earlier observations, polling delay and a later blocked read', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let now = 1000, calls = 0;
  t.mock.method(performance, 'now', () => now);
  const client = createClient('https://deadline.test', { fetch: async () => ++calls === 1 ? Response.json(build('build/1', 'queued')) : new Promise<Response>(() => {}) });
  const rejected = assert.rejects(client.deployments.waitForBuild({ id: 'build/1' }, { timeoutMs: 100, minDelayMs: 20, maxDelayMs: 20 }), error =>
    error instanceof WaiterError && error.code === 'timeout' && error.polls === 2 && error.lastObserved?.status === 'queued');
  await nextTurn(); now += 60; t.mock.timers.tick(60); await nextTurn(); assert.equal(calls, 2);
  now += 39; t.mock.timers.tick(39); await nextTurn(); assert.equal(calls, 2);
  now++; t.mock.timers.tick(1); await rejected;
});

test('a Retry-After wait consumes the same overall deadline', { timeout: 5000 }, async () => {
  let calls = 0;
  const client = createClient('https://retry-after.test', { retry: { maxAttempts: 3, backoffMs: 1 }, fetch: async () => {
    calls++; return new Response('{}', { status: 503, headers: { 'Retry-After': '60' } });
  } });
  await assert.rejects(client.deployments.waitForBuild({ id: 'build/1' }, { timeoutMs: 20 }), error => error instanceof WaiterError && error.code === 'timeout' && error.polls === 1);
  assert.equal(calls, 1);
});

test('HTTP errors stop polling with their original typed error and retire timers/listeners', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  for (const [status, ErrorType] of [[404, ErrNotFound], [401, ErrUnauthorized]] as const) {
    const controller = new AbortController();
    const signals: AbortSignal[] = [];
    let calls = 0;
    const client = createClient('https://http-error.test', { fetch: async (_url, init) => {
      calls++; signals.push(init!.signal!); return Response.json({ code: status === 404 ? 'not_found' : 'unauthorized', tx_id: 'original-tx' }, { status });
    } });
    await assert.rejects(client.deployments.waitForBuild({ id: 'build/1' }, { timeoutMs: 100, signal: controller.signal }), error => error instanceof ErrorType && error.txId === 'original-tx');
    t.mock.timers.tick(200);
    assert.equal(calls, 1); assert.equal(getEventListeners(controller.signal, 'abort').length, 0);
    assert.ok(signals.every(signal => !signal.aborted));
  }
});

test('successful waits clean up deadlines and capture endpoint and credentials across rotations', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const releaseFirst = deferred(), entered = deferred(), controller = new AbortController();
  const seen: Array<{ url: string; token: string | null; signal: AbortSignal }> = [];
  const client = createClient('https://before.test', { token: 'before', fetch: async (url, init) => {
    seen.push({ url: String(url), token: new Headers(init?.headers).get('Authorization'), signal: init!.signal! });
    if (seen.length === 1) { entered.resolve(); await releaseFirst.promise; }
    return Response.json(build('build/1', seen.length === 1 ? 'queued' : 'succeeded'));
  } });
  const options = { ...fast, signal: controller.signal, maxPolls: 2 };
  const pending = client.deployments.waitForBuild({ id: 'build/1' }, options);
  await entered.promise;
  client.setToken('after'); client.setBaseURL('https://after.test'); options.maxPolls = 1;
  releaseFirst.resolve(); await nextTurn(); t.mock.timers.tick(1);
  await pending;
  assert.ok(seen.every(value => value.url.startsWith('https://before.test/') && value.token === 'Bearer before'));
  t.mock.timers.tick(3000); assert.ok(seen.every(value => !value.signal.aborted));
  assert.equal(getEventListeners(controller.signal, 'abort').length, 0);
  await client.deployments.waitForBuild({ id: 'build/1' }, fast);
  assert.ok(seen[2]!.url.startsWith('https://after.test/')); assert.equal(seen[2]!.token, 'Bearer after');
});

test('one timed-out waiter leaves its concurrent sibling running', { timeout: 5000 }, async () => {
  const finish = deferred();
  let siblingDone = false;
  const client = createClient('https://siblings.test', { fetch: async url => {
    if (String(url).endsWith('/short')) return new Promise<Response>(() => {});
    await finish.promise; return Response.json(build('long'));
  } });
  const short = client.deployments.waitForBuild({ id: 'short' }, { timeoutMs: 10 });
  const long = client.deployments.waitForBuild({ id: 'long' }, { timeoutMs: 2000 }).then(value => { siblingDone = true; return value; });
  await assert.rejects(short, error => error instanceof WaiterError && error.code === 'timeout');
  assert.equal(siblingDone, false); finish.resolve(); assert.equal((await long).id, 'long');
});

test('concurrent HTTP waiters keep IDs, credentials, budgets and diagnostics isolated', { timeout: 10_000 }, async t => {
  const originalFetch = globalThis.fetch, originalConfig = { ...OpenAPI };
  const counts = new Map<string, number>();
  const server = createServer(async (req, res) => {
    await nextTurn(); assert.equal(req.method, 'GET');
    const id = req.url!.split('/').at(-1)!;
    const n = Number(id.slice(1)), count = (counts.get(id) ?? 0) + 1; counts.set(id, count);
    assert.equal(req.headers.authorization, n % 2 ? 'Bearer b' : 'Bearer a');
    res.writeHead(200, { 'Content-Type': 'application/json', 'X-Request-ID': `11111111-1111-4111-8111-${String(n).padStart(12, '0')}` });
    res.end(JSON.stringify(build(id, count === 1 ? 'queued' : 'succeeded')));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(async () => { const closed = once(server, 'close'); server.close(); server.closeAllConnections(); await closed; });
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  const base = `http://127.0.0.1:${address.port}`;
  const clients = [createClient(base, { token: 'a' }), createClient(base, { token: 'b' })];
  await Promise.all(Array.from({ length: 20 }, async (_, i) => {
    const reports: ResponseInfo[] = [];
    const pending = clients[i % 2]!.deployments.waitForBuild({ id: `r${i}` }, { ...fast, maxPolls: i % 2 ? 2 : 1, onResponse: info => { reports.push(info); } });
    if (i % 2) assert.equal((await pending).status, 'succeeded');
    else await assert.rejects(pending, error => error instanceof WaiterError && error.code === 'poll_limit');
    assert.equal(reports.length, i % 2 ? 2 : 1);
    assert.ok(reports.every(info => info.requestId?.endsWith(String(i).padStart(12, '0'))));
  }));
  assert.equal([...counts.values()].reduce((a, b) => a + b, 0), 30);
  assert.equal(globalThis.fetch, originalFetch); assert.deepEqual(OpenAPI, originalConfig);
});

test('typed waiter exports retain required budgets, generated results and existing callers', () => {
  const client = createClient('https://types.test');
  assert.ok(Object.isFrozen(client.deployments));
  if (false) {
    const helpers: DeploymentWaiters = client.deployments;
    const a: Promise<BuildResponse> = helpers.waitForBuild({ id: 'build' }, { timeoutMs: 1000 });
    const b: Promise<ReleaseTimeline> = createPreviewClient('https://types.test').deployments.waitForDeployment({ id: 'deployment' }, { timeoutMs: 1000, stopOnBlocked: false });
    const original: CancelablePromise<BuildResponse> = client.deployments.getBuild({ id: 'build' });
    const options: BuildWaitOptions = { timeoutMs: 1000, onProgress: (value, info) => {
      const status: BuildResponse['status'] = value.status; const poll: number = info.poll; void status; void poll;
      // @ts-expect-error progress snapshots are readonly
      value.status = 'succeeded';
    } };
    void a; void b; void original; void options;
    // @ts-expect-error total budget is required
    helpers.waitForBuild({ id: 'build' }, {});
    // @ts-expect-error ID is required
    helpers.waitForDeployment({}, { timeoutMs: 1000 });
    // @ts-expect-error limits are numeric
    helpers.waitForBuild({ id: 'build' }, { timeoutMs: 1000, maxPolls: '2' });
    // @ts-expect-error preview surface remains excluded
    client.flags;
  }
});
