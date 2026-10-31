import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, ProjectsService, type ProjectPromotionHealth } from '../src/preview.js';

test('promotion status preserves queued admission and its separate deadline', async (t) => {
  const queued = '2026-10-15T12:00:00Z', deadline = '2026-10-15T12:15:00Z';
  const health: ProjectPromotionHealth = {
    phase: 'queued', generation: 1, started_at: queued, deadline,
    admission: { queued_at: queued, wait_deadline: deadline, reason: 'fleet_capacity' },
    policy: { probes: [{ app_id: 'app', slug: 'api', method: 'GET', path: '/health' }] },
    verdict: 'unknown', healthy_rounds: 0, regressed_rounds: 0,
  };
  const server = createServer((req, res) => {
    assert.equal(req.url, '/v1/projects/shop/environments/production/promotions/promotion');
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ promotion_id: 'promotion', status: 'running', health }));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(err => err ? reject(err) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token' });
  const status = await ProjectsService.getProjectEnvironmentPromotionStatus({ slug: 'shop', environment: 'production', promotion: 'promotion' });
  assert.deepEqual(status.health, health);
  assert.equal(status.health?.admission?.started_at, undefined);
});
