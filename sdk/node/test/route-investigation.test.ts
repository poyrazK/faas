import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {resolve} from 'node:path';
import {createServer} from 'node:http';
import {once} from 'node:events';
import {AppsService, FaaSClient, type RouteHealthInvestigation} from '../src/index.js';

test('route investigation binds customer signal and retains weighted examples', async (t) => {
  const payload: RouteHealthInvestigation = JSON.parse(readFileSync(resolve('../../tests/fixtures/route-investigation.json'), 'utf8'));
  const server = createServer((req, res) => {
    const url = new URL(req.url!, 'http://localhost');
    assert.equal(req.method, 'GET');
    assert.equal(url.pathname, '/v1/apps/demo/route-health/deployments/candidate/investigation');
    assert.equal(url.searchParams.get('method'), 'POST');
    assert.equal(url.searchParams.get('path'), '/checkout');
    assert.equal(url.searchParams.get('status_code'), '403');
    assert.equal(url.searchParams.get('customer_id'), payload.selection.customer_id);
    assert.equal(url.searchParams.get('customer_group_by'), 'consumer');
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify(payload));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, {token: 'test', retry: {maxAttempts: 1, backoffMs: 0}});
  const result = await AppsService.getRouteHealthInvestigation({slug: 'demo', deployment: 'candidate', method: 'POST', path: '/checkout', statusCode: 403, customerGroupBy: 'consumer', customerId: payload.selection.customer_id!});
  assert.deepEqual(result, payload);
  assert.equal(result.status, 'regressed');
  assert.equal(result.report.status, 'healthy');
  assert.equal(result.windows[0]!.candidate.examples[0]!.represented_requests, 20);
});
