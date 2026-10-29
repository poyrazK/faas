import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {once} from 'node:events';
import {AppsService, FaaSClient, type IssueCompoundAlertRule} from '../src/preview.js';

test('compound management preserves the AND thresholds and one shared window', async t => {
  const rule: IssueCompoundAlertRule = {key: 'payments', enabled: true, environment: 'production', minimum_events: 20, minimum_customers: 3, window_seconds: 600, cooldown_seconds: 1800};
  const methods: string[] = [];
  const server = createServer(async (req, res) => {
    methods.push(req.method!); res.setHeader('Content-Type', 'application/json');
    if (req.method === 'GET') { assert.equal(req.url, '/v1/apps/payments/issue-compound-alert-policy'); res.end(JSON.stringify({revision: 3, rules: [rule]})); return; }
    const chunks: Buffer[] = []; for await (const chunk of req) chunks.push(Buffer.from(chunk));
    const body = JSON.parse(Buffer.concat(chunks).toString());
    if (req.method === 'PUT') { assert.deepEqual(body, {expected_revision: 2, rules: [rule]}); res.end(JSON.stringify({revision: 3, rules: [rule]})); return; }
    assert.equal(req.url, '/v1/apps/payments/issue-compound-alert-policy/preview');
    assert.deepEqual(body, {issue_id: 'one', rule}); res.end(JSON.stringify({allowed: false, events_matched: true, customers_matched: false, reason: 'customer_threshold'}));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, {token: 'test', retry: {maxAttempts: 1, backoffMs: 0}});
  assert.deepEqual((await AppsService.getIssueCompoundAlertPolicy({slug: 'payments'})).rules, [rule]);
  await AppsService.setIssueCompoundAlertPolicy({slug: 'payments', requestBody: {expected_revision: 2, rules: [rule]}});
  const preview = await AppsService.previewIssueCompoundAlert({slug: 'payments', requestBody: {issue_id: 'one', rule}});
  assert.equal(preview.events_matched, true); assert.equal(preview.customers_matched, false);
  assert.deepEqual(methods, ['GET', 'PUT', 'POST']);
});
