// adr: 20261105102920
import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, ProjectsService, type EnvironmentRollbackReceipt } from '../src/preview.js';

for (const company of [false, true]) {
  test(`original ${company ? 'company' : 'personal'} environment rollback receipt scopes write headers`, async (t) => {
    const attempt = '3e9f323aade6442b8444c91da107fe44';
    const promotion = '3e9f323a-ade6-442b-8444-c91da107fe44';
    const receipt: EnvironmentRollbackReceipt = {
      attempt_id: attempt, request_hash: 'a'.repeat(64), promotion_id: promotion,
      promotion_hash: 'b'.repeat(64), project_id: 'e377519c-cbca-4dfa-a3e7-26b1321e0de4',
      from_environment_id: '8677f406-6b97-4b24-bf26-cf795e040713',
      environment_id: '142b7504-f03a-4ee2-aeb3-14d922a845d4', accepted_at: new Date().toISOString(),
    };
    const prefix = company ? '/v1/orgs/team/projects' : '/v1/projects';
    const base = `${prefix}/shop/environments/production/promotions/${promotion}`;
    const server = createServer((req, res) => { void (async () => {
      res.setHeader('Content-Type', 'application/json');
      if (req.method === 'POST') {
        assert.equal(req.url, `${base}/rollback`);
        assert.equal(req.headers['environment-rollback-attempt-id'], attempt);
        assert.equal(req.headers['environment-rollback-request-hash'], receipt.request_hash);
        assert.equal(req.headers['idempotency-key'], 'original');
        let body = ''; for await (const chunk of req) body += String(chunk);
        assert.equal(body, '');
        res.end(JSON.stringify({ promotion_id: promotion, rollback_status: 'rolling_back' }));
      } else {
        assert.equal(req.method, 'GET');
        assert.equal(req.url, `${base}/rollback-attempts/${attempt}`);
        assert.equal(req.headers['environment-rollback-attempt-id'], undefined);
        assert.equal(req.headers['environment-rollback-request-hash'], undefined);
        assert.equal(req.headers['idempotency-key'], undefined);
        res.end(JSON.stringify(receipt));
      }
    })().catch(error => { res.statusCode = 500; res.end(String(error)); }); });
    server.listen(0, '127.0.0.1'); await once(server, 'listening');
    t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
    const address = server.address(); assert.ok(address && typeof address !== 'string');
    new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture', retry: { maxAttempts: 1, backoffMs: 0 } });
    const args = { slug: 'shop', environment: 'production', promotion, idempotencyKey: 'original', environmentRollbackAttemptId: attempt, environmentRollbackRequestHash: receipt.request_hash };
    const accepted = company
      ? await ProjectsService.rollbackOrgProjectEnvironmentPromotion({ ...args, organization: 'team' })
      : await ProjectsService.rollbackProjectEnvironmentPromotion(args);
    assert.equal(accepted.promotion_id, promotion);
    const selector = { slug: 'shop', environment: 'production', promotion, attemptId: attempt };
    const recovered = company
      ? await ProjectsService.getOrgEnvironmentRollbackReceipt({ ...selector, organization: 'team' })
      : await ProjectsService.getEnvironmentRollbackReceipt(selector);
    assert.deepEqual(recovered, receipt);
  });
}
