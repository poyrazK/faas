import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {once} from 'node:events';
import {AppsService, FaaSClient, type RouteHealthReport} from '../src/index.js';

test('customer health binds advisory options and preserves sparse coverage', async (t) => {
  const payload: RouteHealthReport = {
    app_id: 'app', deployment_id: 'candidate', candidate_commit_sha: '', stable_deployment_id: 'stable',
    stable_commit_sha: '', canary_step: 0, mode: 'enforce', revision: 1, checked_at: '2026-10-03T00:00:00Z',
    coverage: 'observed_only', status: 'healthy', reason: 'comparisons_healthy', minimum_requests: 20, routes: [],
    customers: {group_by: 'consumer', details_included: true, coverage: 'observed_only', status: 'unknown',
      reason: 'customer_evidence_incomplete', customers_limit: 20, routes: [{method: 'GET', path: '/orders',
        observed_customers: 23, customers_truncated: true,
        candidate: {identified_requests: 100, unattributed_requests: 7, unresolved_identity_requests: 3, other_customer_requests: 80},
        stable: {identified_requests: 0, unattributed_requests: 0, unresolved_identity_requests: 0, other_customer_requests: 0},
        customers: [{customer_id: 'customer', health: {method: 'GET', path: '/orders', status: 'unknown', reason: 'comparisons_incomplete_or_unsettled',
          windows: [{start: '2026-10-02T23:57:00Z', end: '2026-10-02T23:58:00Z', status: 'unknown', reason: 'insufficient_requests',
            candidate: {requests: 20, server_errors: 10, error_rate: 0.5, p95_latency_ms: 500}, stable: {requests: 0, server_errors: 0, error_rate: 0}}]}}]}]},
  };
  const server = createServer((req, res) => {
    const url = new URL(req.url!, 'http://localhost');
    assert.equal(req.method, 'GET');
    assert.equal(url.pathname, '/v1/apps/demo/route-health/deployments/candidate');
    assert.equal(url.searchParams.get('customers'), 'true');
    assert.equal(url.searchParams.get('customer_group_by'), 'consumer');
    assert.equal(url.searchParams.get('customer_details'), 'true');
    res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(payload));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, {token: 'test', retry: {maxAttempts: 1, backoffMs: 0}});
  const result = await AppsService.getRouteHealthReport({slug: 'demo', deployment: 'candidate', customers: true, customerGroupBy: 'consumer', customerDetails: true});
  assert.deepEqual(result, payload);
});
