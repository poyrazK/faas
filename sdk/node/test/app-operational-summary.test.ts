import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {resolve} from 'node:path';
import {createServer} from 'node:http';
import {once} from 'node:events';
import {AppsService, FaaSClient, type AppOperationalSummary} from '../src/index.js';

test('summary preserves unknown health, blocked recovery and unavailable sources', async (t) => {
  const summary: AppOperationalSummary = JSON.parse(readFileSync(resolve('../../tests/fixtures/app-operational-summary.json'), 'utf8'));
  let calls = 0;
  const server = createServer((req, res) => {
    calls++;
    assert.equal(req.method, 'GET');
    assert.equal(req.url, '/v1/apps/demo/operational-summary');
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify(summary));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, {token: 'test', retry: {maxAttempts: 1, backoffMs: 0}});
  const result = await AppsService.getAppOperationalSummary({slug: 'demo'});
  assert.deepEqual(result, summary);
  assert.equal(result.monitoring.status, 'unknown');
  assert.equal(result.recovery.rollbacks[0]?.status, 'blocked');
  assert.equal(result.recovery.restarts_available, false);
  assert.equal(result.recovery.rollbacks_truncated, true);
  assert.equal(calls, 1);
});

test('scoped summary preserves app-wide worker retry and pool observations', async (t) => {
  const summary: AppOperationalSummary = JSON.parse(readFileSync(resolve('../../tests/fixtures/app-operational-summary.json'), 'utf8'));
  summary.recovery.workers = {
    available: true, eligible: true, scope: 'app', status: 'waiting_capacity', failures: 3,
    next_attempt_at: '2026-10-17T22:30:00Z',
    pools: [{scope: 'staging', observed_at: '2026-10-17T22:00:00Z', stale: true,
      desired: 2, running: 1, starting: 0, draining: 0,
      blockers: [{code: 'account_worker_limit', resource: 'replicas', used: 3, limit: 3,
        observed_at: '2026-10-17T21:59:00Z', stale: true},
      {code: 'retry_cooldown', observed_at: '2026-10-17T22:00:00Z', retry_at: '2026-10-17T22:30:00Z'}]}],
  };
  const server = createServer((req, res) => {
    assert.equal(req.url, '/v1/apps/demo/operational-summary?environment=staging');
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify(summary));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, {token: 'test', retry: {maxAttempts: 1, backoffMs: 0}});
  const result = await AppsService.getAppOperationalSummary({slug: 'demo', environment: 'staging'});
  assert.deepEqual(result.recovery.workers, summary.recovery.workers);
});
