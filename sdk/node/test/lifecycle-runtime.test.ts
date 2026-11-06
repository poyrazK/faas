import assert from 'node:assert/strict';
import {once} from 'node:events';
import {createServer, request, type Server} from 'node:http';
import type {AddressInfo} from 'node:net';
import {test, type TestContext} from 'node:test';
import {DEFAULT_AFTER_RESTORE_PATH, DEFAULT_BEFORE_CHECKPOINT_PATH, LifecycleHooks} from '../src/lifecycle-runtime.js';

async function serve(t: TestContext, hooks: LifecycleHooks): Promise<number> {
  const server: Server = createServer((req, res) => {
    if (!hooks.handle(req, res)) res.writeHead(418).end();
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>(resolve => server.close(() => resolve())));
  return (server.address() as AddressInfo).port;
}

function post(port: number, path: string, headers: Record<string, string> = {}, method = 'POST'): Promise<number> {
  return new Promise((resolve, reject) => {
    const req = request({host: '127.0.0.1', port, path, method, headers}, res => {
      res.resume();
      res.on('end', () => resolve(res.statusCode ?? 0));
    });
    req.on('error', reject);
    req.end();
  });
}

test('serves only marked POSTs on the hook paths', async t => {
  const hooks = new LifecycleHooks();
  let restored = 0;
  hooks.onAfterRestore(() => { restored++; });
  const port = await serve(t, hooks);
  assert.equal(await post(port, DEFAULT_AFTER_RESTORE_PATH, {'x-faas-after-restore': '1'}), 204);
  assert.equal(await post(port, DEFAULT_AFTER_RESTORE_PATH), 403);
  assert.equal(await post(port, DEFAULT_AFTER_RESTORE_PATH, {'x-faas-before-checkpoint': '1'}), 403);
  assert.equal(await post(port, DEFAULT_AFTER_RESTORE_PATH, {'x-faas-after-restore': '1'}, 'GET'), 405);
  assert.equal(await post(port, '/v1/query'), 418);
  assert.equal(restored, 1);
});

test('a throwing or slow callback fails the hook', async t => {
  const failing = new LifecycleHooks().onBeforeCheckpoint(() => { throw new Error('flush failed'); });
  const slow = new LifecycleHooks({budgetMs: 30}).onBeforeCheckpoint(signal => new Promise(resolve => {
    const timer = setTimeout(resolve, 500);
    signal.addEventListener('abort', () => clearTimeout(timer));
  }));
  for (const hooks of [failing, slow]) {
    const port = await serve(t, hooks);
    assert.equal(await post(port, DEFAULT_BEFORE_CHECKPOINT_PATH, {'x-faas-before-checkpoint': '1'}), 500);
  }
});

test('overlapping deliveries share one run', async () => {
  const hooks = new LifecycleHooks();
  let runs = 0;
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  hooks.onAfterRestore(async () => { runs++; await gate; });
  const first = hooks.run('after_restore');
  const second = hooks.run('after_restore');
  release();
  await Promise.all([first, second]);
  assert.equal(runs, 1);
  await hooks.run('after_restore');
  assert.equal(runs, 2);
});

// Review finding 10: a timed-out delivery must not let a retry start the
// callbacks again while the first run is still going.
test('a retry after the budget waits for the still-running run', async () => {
  const hooks = new LifecycleHooks({budgetMs: 20});
  let runs = 0, running = 0, maxRunning = 0;
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  hooks.onAfterRestore(async () => {
    runs++; running++; maxRunning = Math.max(maxRunning, running);
    await gate; // ignores the abort signal, like a blocking reconnect
    running--;
  });
  await assert.rejects(hooks.run('after_restore'), /budget/);
  await assert.rejects(hooks.run('after_restore'), /budget/);
  release();
  await new Promise(resolve => setTimeout(resolve, 10));
  assert.equal(runs, 1);
  assert.equal(maxRunning, 1);
  await hooks.run('after_restore').catch(() => {}); // a fresh run once the first settled
  assert.equal(runs, 2);
});

test('rejects a non-positive budget', () => {
  assert.throws(() => new LifecycleHooks({budgetMs: 0}), /budgetMs/);
});
