import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, ProjectsService, type ProjectPromotionControl } from '../src/index.js';

test('promotion abort and recovery preserve generation and durable receipts', async (t) => {
  const request = { request_id: 'a'.repeat(32), expected_generation: 7 };
  const now = new Date().toISOString();
  const receipt: ProjectPromotionControl = {
    request, action: 'abort', status: 'pending', operator_account_id: 'account', requested_at: now, deadline: now,
  };
  let calls = 0;
  const server = createServer((req, res) => { void (async () => {
    const action = calls++ === 0 ? 'abort' : 'recover';
    assert.equal(req.method, 'POST');
    assert.equal(req.url, `/v1/projects/shop/environments/production/promotions/promotion/${action}`);
    let body = ''; for await (const chunk of req) body += String(chunk);
    assert.deepEqual(JSON.parse(body), request);
    res.setHeader('Content-Type', 'application/json');
    res.statusCode = action === 'abort' ? 202 : 200;
    res.end(JSON.stringify({ ...receipt, action, status: action === 'abort' ? 'pending' : 'restored' }));
  })().catch(error => { res.statusCode = 500; res.end(String(error)); }); });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const args = { slug: 'shop', environment: 'production', promotion: 'promotion', requestBody: request };
  assert.deepEqual(await ProjectsService.abortProjectEnvironmentPromotion(args), receipt);
  const restored = await ProjectsService.recoverProjectEnvironmentPromotion(args);
  assert.equal(restored.status, 'restored');
  assert.deepEqual(restored.request, request);
  assert.equal(calls, 2);
});
