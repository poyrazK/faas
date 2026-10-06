import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';

import { AppsService, FaaSClient, type RouteCustomerUsageResponse } from '../src/index.js';

test('route customer usage preserves deployment scope and incomplete coverage', async (t) => {
  const deployment = '11111111-1111-4111-8111-111111111111';
  const at = '2026-10-02T00:00:00Z';
  const payload: RouteCustomerUsageResponse = {
    slug: 'demo', deployment_id: deployment, from: '2026-10-01T00:00:00Z', until: at, as_of: at,
    coverage: 'observed_only', window_clamped: true, routes_limit: 200, routes_truncated: true, customers_limit: 20,
    routes: [{route: 'GET /orders', method: 'GET', requests: 15, identified_requests: 10,
      anonymous_requests: 5, unresolved_identity_requests: 0, consumer_count: 3, platform_tenant_count: 1,
      last_observed_at: at, customers_truncated: true, other_customer_requests: 2,
      customers: [{platform_tenant_id: '33333333-3333-4333-8333-333333333333', requests: 8, last_observed_at: at}]}],
  };
  const server = createServer((req, res) => {
    const url = new URL(req.url!, 'http://localhost');
    assert.equal(req.method, 'GET');
    assert.equal(url.pathname, '/v1/apps/demo/analytics/route-customers');
    assert.equal(url.searchParams.get('deployment_id'), deployment);
    assert.equal(url.searchParams.get('since'), '7d');
    assert.equal(url.searchParams.get('until'), at);
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify(payload));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, {token: 'test', retry: {maxAttempts: 1, backoffMs: 0}});
  const result = await AppsService.getAppRouteCustomerUsage({slug: 'demo', deploymentId: deployment, since: '7d', until: at});
  assert.deepEqual(result, payload);
  assert.equal(result.routes[0]!.customers[0]!.consumer_id, undefined);
});
