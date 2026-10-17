import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {once} from 'node:events';
import {AppsService, FaaSClient, type IssueLifecycleAlertPolicy} from '../src/index.js';

test('lifecycle policy and draft preview preserve scope, revision and cooldown evidence', async t => {
  const policy: IssueLifecycleAlertPolicy = {revision: 3, environments: ['production'], cooldown_seconds: 1800};
  const methods: string[] = [];
  const server = createServer(async (req, res) => {
    methods.push(req.method!);
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'GET') {
      assert.equal(req.url, '/v1/apps/payments/issue-lifecycle-alert-policy');
      res.end(JSON.stringify(policy)); return;
    }
    const chunks: Buffer[] = [];
    for await (const chunk of req) chunks.push(Buffer.from(chunk));
    const body = JSON.parse(Buffer.concat(chunks).toString());
    if (req.method === 'PUT') {
      assert.equal(req.url, '/v1/apps/payments/issue-lifecycle-alert-policy');
      assert.deepEqual(body, {expected_revision: 2, environments: ['production'], cooldown_seconds: 1800});
      res.end(JSON.stringify(policy)); return;
    }
    assert.equal(req.url, '/v1/apps/payments/issue-lifecycle-alert-policy/preview');
    assert.deepEqual(body, {issue_id: 'one', event: 'regressed', environments: [], cooldown_seconds: 60});
    res.end(JSON.stringify({issue_id: 'one', event: 'regressed', environment: 'production', policy_revision: 3, allowed: false, reason: 'cooldown', next_eligible_at: '2026-10-17T14:01:00Z'}));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, {token: 'test', retry: {maxAttempts: 1, backoffMs: 0}});
  assert.deepEqual(await AppsService.getIssueLifecycleAlertPolicy({slug: 'payments'}), policy);
  assert.deepEqual(await AppsService.setIssueLifecycleAlertPolicy({slug: 'payments', requestBody: {expected_revision: 2, environments: ['production'], cooldown_seconds: 1800}}), policy);
  const preview = await AppsService.previewIssueLifecycleAlert({slug: 'payments', requestBody: {issue_id: 'one', event: 'regressed', environments: [], cooldown_seconds: 60}});
  assert.equal(preview.allowed, false); assert.equal(preview.next_eligible_at, '2026-10-17T14:01:00Z');
  assert.deepEqual(methods, ['GET', 'PUT', 'POST']);
});
