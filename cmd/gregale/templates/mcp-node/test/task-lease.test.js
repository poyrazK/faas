import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { createMcpTaskLeaseDeadline } from '../task-lease.js';
import { createMcpTaskRuntime } from '../tasks.js';

async function flush() { for (let i = 0; i < 20; i++) await Promise.resolve(); }

test('lease renewal excludes database response latency and cannot revive expired ownership', t => {
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: 0 });
  let expirations = 0;
  const lease = createMcpTaskLeaseDeadline({ leaseMs: 30000, onExpired: () => expirations++, now: () => Date.now() });
  t.after(() => lease.close());
  t.mock.timers.tick(20000);
  lease.renew(5000); // A request begun at 5 s returned at 20 s.
  t.mock.timers.tick(14999);
  assert.equal(expirations, 0);
  t.mock.timers.tick(1);
  assert.equal(expirations, 1);
  lease.renew(Date.now());
  t.mock.timers.tick(60000);
  assert.equal(expirations, 1);
});

test('a late response cannot renew ownership before a delayed timer callback runs', t => {
  let now = 0, expirations = 0;
  const lease = createMcpTaskLeaseDeadline({ leaseMs: 30000, now: () => now, onExpired: () => expirations++ });
  t.after(() => lease.close());
  now = 31000;
  lease.renew(now);
  assert.equal(expirations, 1);
  lease.renew(now);
  assert.equal(expirations, 1);
});

for (const outage of ['rejected', 'blocked', 'recovered']) {
  test(`handler lease deadline survives ${outage} heartbeat requests`, async t => {
    t.mock.timers.enable({ apis: ['Date', 'setTimeout', 'setInterval'], now: 0 });
    t.mock.method(performance, 'now', () => Date.now());
    let claimed = false, calls = 0, writes = 0, signal, lateHeartbeat;
    const task = { task_id: randomUUID(), lease_token: randomUUID(), tool_name: 'build_report', handler_version: '1', arguments: {}, attempt_count: 1 };
    const store = {
      async initialize() {},
      async claim() { if (claimed) return null; claimed = true; return task; },
      async heartbeat() {
        calls++;
        if (outage === 'blocked') return new Promise(resolve => { lateHeartbeat = resolve; });
        if (outage === 'recovered' && calls === 3) return { owned: true, cancelRequested: false };
        throw new Error('test database unavailable');
      },
      async complete() { writes++; }, async fail() { writes++; },
    };
    const runtime = createMcpTaskRuntime({ store, handlers: { build_report: { version: '1', execute(_args, context) {
      signal = context.signal;
      return new Promise(resolve => signal.addEventListener('abort', () => resolve({ content: [] }), { once: true }));
    } } } });
    await runtime.start();
    t.after(() => runtime.stop());
    const deadline = outage === 'recovered' ? 45000 : 30000;
    for (let elapsed = 0; elapsed < deadline - 1000; elapsed += 1000) { t.mock.timers.tick(1000); await flush(); }
    assert.equal(signal.aborted, false, 'transient failures do not cancel a proven live lease');
    t.mock.timers.tick(1000); await flush();
    assert.equal(signal.aborted, true, 'execution is cancelled at the last proven deadline');
    lateHeartbeat?.({ owned: true, cancelRequested: false }); await flush();
    assert.equal(writes, 0, 'lost ownership suppresses terminal writes even after a late renewal');
    assert.deepEqual(await runtime.stop(), { timedOut: false });
  });
}

test('shutdown includes an unresolved claim in its deadline and releases a late response', async t => {
  t.mock.timers.enable({ apis: ['Date', 'setTimeout', 'setInterval'], now: 0 });
  let claimReady, respond, released = 0, executions = 0;
  const ready = new Promise(resolve => { claimReady = resolve; });
  const response = new Promise(resolve => { respond = resolve; });
  const task = { task_id: randomUUID(), lease_token: randomUUID(), tool_name: 'build_report', handler_version: '1' };
  const store = { async initialize() {}, async claim() { claimReady(); return response; }, async releaseMany(tasks) { released += tasks.length; } };
  const runtime = createMcpTaskRuntime({ store, shutdownTimeoutMs: 1000, handlers: { build_report: { version: '1', async execute() { executions++; return { content: [] }; } } } });
  const starting = runtime.start();
  await ready;
  const stopping = runtime.stop();
  await flush();
  t.mock.timers.tick(1000); await flush();
  assert.deepEqual(await stopping, { timedOut: true });
  respond(task); await starting;
  assert.equal(released, 1);
  assert.equal(executions, 0);
});
