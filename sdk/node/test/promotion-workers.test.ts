import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, ProjectsService, type ProjectPromotionHealth, type ProjectReleaseWorker, type ProjectWorkerHealth } from '../src/preview.js';

test('promotion status preserves independent worker evidence and the complete policy', async (t) => {
  const policy: ProjectReleaseWorker[] = ['billing', 'mail', 'webhooks'].map(slug => ({
    app_id: slug, slug, binding_id: `${slug}-binding`, binding_hash: 'a'.repeat(64),
    previous_deployment_id: `${slug}-previous`, drain_seconds: 30, status: 'building',
  }));
  const sample = { attempts: 10, failures: 0, incomplete: 0, duration_ms: 100, retries: 0, queue_delay_ms: 0 };
  const healthy: ProjectWorkerHealth = {
    status: 'healthy', candidate: sample, baseline: sample,
    pending_jobs: 0, oldest_pending_age_ms: 0, baseline_pending_jobs: 0, baseline_oldest_pending_age_ms: 0,
  };
  const health: ProjectPromotionHealth = {
    phase: 'verifying', generation: 4, started_at: '2026-10-16T12:00:00Z', deadline: '2026-10-16T12:15:00Z',
    policy: { probes: [{ app_id: 'api', slug: 'api', method: 'GET', path: '/health' }], workers: policy },
    workers: policy.map((worker, i) => ({
      worker: { ...worker, status: 'active', deployment_id: `${worker.slug}-candidate`, instance_id: `${worker.slug}-runtime` },
      health: i === 2 ? { ...healthy, status: 'insufficient_evidence', candidate: { ...sample, attempts: 0 } } : healthy,
    })),
    verdict: 'unknown', healthy_rounds: 0, regressed_rounds: 0,
  };
  const server = createServer((_req, res) => {
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ promotion_id: 'promotion', status: 'running', health }));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(err => err ? reject(err) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token' });
  const status = await ProjectsService.getProjectEnvironmentPromotionStatus({ slug: 'shop', environment: 'production', promotion: 'promotion' });
  assert.deepEqual(status.health, health);
  assert.equal(status.health?.worker, undefined);
  assert.equal(status.health?.policy.worker, undefined);
  assert.equal(status.health?.workers?.[2]?.health.status, 'insufficient_evidence');
});
