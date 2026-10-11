import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {once} from 'node:events';
import {AppsService, FaaSClient, type ProfileDeploymentPolicy, type ProfileDeploymentCheck} from '../src/index.js';

test('automatic profiling operations retain revision zero and pending windows', async t => {
  const config = {enabled: true, runtime: 'node24', window_seconds: 300, warmup_seconds: 120, options: {relative_increase_percent: 20, absolute_increase_cpu_per_second: .01, minimum_profiles: 3, minimum_coverage_ratio: .8}};
  let policy: ProfileDeploymentPolicy = {app_id: 'app', revision: 0, config};
  const at = '2026-10-08T12:00:00.123Z';
  const check: ProfileDeploymentCheck = {deployment_id: 'dep', app_id: 'app', scope: 'prod', policy_revision: 1, config, candidate: {deployment_id: 'dep', runtime: 'node24', start: at, end: '2026-10-08T12:05:00.123Z'}, status: 'queued', reason: 'Waiting for capture', attempts: 0, next_attempt_at: at, created_at: at};
  const server = createServer((req, res) => {
    assert.equal(req.headers.authorization, 'Bearer token');
    let body = '';
    req.on('data', data => {body += data});
    req.on('end', () => {
      let result: unknown = check;
      if (req.url!.endsWith('/deployment-policy')) {
        if (req.method === 'PUT') {
          assert.deepEqual(JSON.parse(body), {expected_revision: 0, config});
          policy = {...policy, revision: 1, updated_at: at};
        }
        result = policy;
      } else if (req.url!.endsWith('/deployment-checks')) result = {checks: [check]};
      res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(result));
    });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(err => err ? reject(err) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, {token: 'token', retry: {maxAttempts: 1, backoffMs: 0}});
  assert.equal((await AppsService.getProfileDeploymentPolicy({slug: 'demo'})).revision, 0);
  assert.equal((await AppsService.saveProfileDeploymentPolicy({slug: 'demo', requestBody: {expected_revision: 0, config}})).revision, 1);
  assert.deepEqual(await AppsService.listProfileDeploymentChecks({slug: 'demo'}), {checks: [check]});
  assert.deepEqual(await AppsService.getProfileDeploymentCheck({slug: 'demo', id: 'dep'}), check);
});
