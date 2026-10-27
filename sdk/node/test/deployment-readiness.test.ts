import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, ProjectsService, type ProjectDeploymentCheckRequest, type ProjectDeploymentReadiness } from '../src/preview.js';

test('deployment readiness preserves unknown checks and quota evidence', async (t) => {
  const request: ProjectDeploymentCheckRequest = {
    environment: 'production', atomic: true, verify: true, workers: true,
    workloads: [{ workload: { name: 'api', root_dir: '', tier: 'compose', command: [], ports: [] }, provided_keys: ['TOKEN'] }],
  };
  const report: ProjectDeploymentReadiness = {
    schema_version: 1, project_slug: 'shop', environment: 'production', status: 'blocked',
    checked_at: '2026-10-16T12:00:00Z', runtime_checked: false,
    checks: [
      { code: 'worker_replica_overlap', status: 'blocked', message: 'Minimum replica overlap', remedy: 'Use sufficient replica quota', observed: 6, limit: 3 },
      { code: 'runtime_inputs_unavailable', status: 'unknown', workload: 'api', message: 'Inputs unavailable', remedy: 'Retry the check' },
    ],
  };
  const server = createServer(async (req, res) => {
    assert.equal(req.method, 'POST');
    assert.equal(req.url, '/v1/projects/shop/deployment-readiness');
    let body = ''; for await (const chunk of req) body += chunk;
    assert.deepEqual(JSON.parse(body), request);
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify(report));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(err => err ? reject(err) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token' });
  const result = await ProjectsService.checkProjectDeployment({ slug: 'shop', requestBody: request });
  assert.deepEqual(result, report);
  assert.equal(result.runtime_checked, false);
  assert.equal(result.state_hash, undefined);
});
