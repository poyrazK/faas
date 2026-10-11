import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {once} from 'node:events';
import {DeploymentsService, FaaSClient, type DeploymentRuntimeResponse, type RuntimeUpgradePreviewResponse} from '../src/index.js';

test('runtime reads preserve unknown provenance and encode preview targets without mutation', async (t) => {
  const target = 'candidate&literal=value';
  const current: DeploymentRuntimeResponse = {deployment_id: 'deployment', status: 'unknown', reason: 'No recorded binding.', current: null, releases: []};
  const runtime = {id: 'candidate', runtime: 'node22', architecture: 'amd64', source_digest: 'sha256:source', guest_init_digest: 'sha256:guest', base_digest: 'sha256:base', layout_version: 'v3', published_at: '2026-10-05T12:00:00Z', qualification: 'not_evaluated'} as RuntimeUpgradePreviewResponse['target'];
  const preview: RuntimeUpgradePreviewResponse = {deployment_id: 'deployment', current: null, target: runtime, disposition: 'blocked', changes: [], blockers: ['Unknown current identity.'], required_steps: [], rebuild_required: false, cold_start_required: false, execution_available: false};
  const server = createServer((req, res) => {
    const url = new URL(req.url!, 'http://localhost');
    assert.equal(req.method, 'GET'); assert.equal(req.headers.authorization, 'Bearer token');
    res.setHeader('Content-Type', 'application/json');
    if (url.pathname.endsWith('upgrade-preview')) {assert.equal(url.searchParams.get('target'), target); res.end(JSON.stringify(preview));}
    else {assert.equal(url.pathname, '/v1/deployments/deployment/runtime');res.end(JSON.stringify(current));}
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();assert.ok(address && typeof address !== 'string');
  const client = new FaaSClient(`http://127.0.0.1:${address.port}`, {token: 'token', retry: {maxAttempts: 1, backoffMs: 0}});
  t.after(() => client.uninstall());
  assert.deepEqual(await DeploymentsService.getDeploymentRuntime({id: 'deployment'}), current);
  assert.deepEqual(await DeploymentsService.previewRuntimeUpgrade({id: 'deployment', target}), preview);
});
