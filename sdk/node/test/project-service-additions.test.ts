import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, ProjectsService, type ProjectSafeRelease } from '../src/preview.js';

test('atomic additions carry explicit intent and return reservation metadata', async (t) => {
  const checkpoint: ProjectSafeRelease = {
    status: 'building', previous_release_id: 'baseline', ttl_seconds: 1800,
    deadline: '2026-10-16T14:00:00Z', add_services: true,
    additions: [{ app_id: 'metrics-app', slug: 'metrics', root_dir: 'metrics', fingerprint: 'a'.repeat(64) }],
  };
  const server = createServer((req, res) => {
    assert.equal(req.url, '/v1/projects/safe-deploy?plan_token=addition-plan');
    assert.equal(req.headers['project-add-services'], 'required');
    assert.equal(req.headers['project-release-mode'], 'atomic');
    assert.equal(req.headers['deployment-attempt-id'], 'a'.repeat(32));
    req.resume();
    req.on('end', () => { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ project_id: 'shop', safe_release: checkpoint })); });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const response = await ProjectsService.safeApplyProject({
    deploymentAttemptId: 'a'.repeat(32), projectAddServices: 'required', projectReleaseMode: 'atomic', planToken: 'addition-plan',
    formData: { project_slug: 'shop', environment: 'production', no_triggers: true, source: new Blob(['source']) },
  });
  assert.deepEqual(response.safe_release, checkpoint);
});
