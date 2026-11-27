import assert from 'node:assert/strict';
import test from 'node:test';
import { startMcpTaskWorkerReceipts } from '../task-worker-receipts.js';

test('worker receipts refresh the resolved process identity until shutdown', () => {
  const service = { workerID: '11111111-1111-4111-8111-111111111111', shutdownTimeoutMs: 120_000 };
  const logs = [];
  let tick, cancelled = 0, unreferenced = false;
  const timer = { unref() { unreferenced = true; } };
  const stop = startMcpTaskWorkerReceipts(service, {
    log: line => logs.push(JSON.parse(line)),
    schedule(callback, interval) { assert.equal(interval, 10_000); tick = callback; return timer; },
    cancel(value) { assert.equal(value, timer); cancelled++; },
  });
  assert.equal(logs.length, 1);
  service.workerID = 'caller mutation'; service.shutdownTimeoutMs = 1;
  tick(); tick();
  assert.equal(logs.length, 3);
  for (const receipt of logs) assert.deepEqual(receipt, {
    event: 'mcp_task_worker_started', workerID: '11111111-1111-4111-8111-111111111111',
    claimFence: true, shutdownTimeoutMs: 120_000,
  });
  assert.equal(unreferenced, true);
  stop(); stop();
  assert.equal(cancelled, 1);
});
