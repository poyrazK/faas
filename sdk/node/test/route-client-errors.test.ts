import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {once} from 'node:events';
import {AppsService, FaaSClient, type RouteHealthClientErrorReport, type RouteHealthReport} from '../src/index.js';

test('watched response selectors and advisory evidence preserve wire types', async (t) => {
  const window = {start: '2026-10-03T00:00:00Z', end: '2026-10-03T00:01:00Z', candidate: {requests: 100, responses: 20, rate: 0.2}, stable: {requests: 100, responses: 0, rate: 0}, status: 'regressed' as const, reason: 'watched_status_rate_increased'};
  const advisory: RouteHealthClientErrorReport = {status: 'regressed', reason: 'consecutive_watched_status_regression', minimum_requests: 20, minimum_responses: 2, rate_floor: 0.05, rate_delta: 0.05, rate_factor: 3,
    statuses: [{status_code: 403, status: 'regressed', reason: 'consecutive_watched_status_regression', windows: [window, {...window, start: window.end, end: '2026-10-03T00:02:00Z'}]}]};
  const payload: RouteHealthReport = {app_id: 'app', deployment_id: 'candidate', candidate_commit_sha: '', stable_deployment_id: 'stable', stable_commit_sha: '', canary_step: 0, mode: 'report', revision: 1, checked_at: '2026-10-03T00:03:00Z', coverage: 'observed_only', status: 'healthy', reason: 'comparisons_healthy', minimum_requests: 20,
    client_error_status: 'regressed', client_error_reason: advisory.reason,
    routes: [{method: 'POST', path: '/checkout', watch_statuses: [403], status: 'healthy', reason: 'comparisons_healthy', windows: [], client_errors: advisory}]};
  const server = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'PUT') {
      let body = ''; for await (const chunk of req) body += chunk;
      const request = JSON.parse(body);
      assert.deepEqual(request.routes[0].watch_statuses, [403, 422]);
      res.end(JSON.stringify({app_id: 'app', mode: 'report', revision: 1, routes: request.routes}));
    } else {
      assert.equal(req.url, '/v1/apps/demo/route-health/deployments/candidate?customers=false&customer_details=false');
      res.end(JSON.stringify(payload));
    }
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, {token: 'test', retry: {maxAttempts: 1, backoffMs: 0}});
  const gate = await AppsService.setRouteHealthGate({slug: 'demo', requestBody: {mode: 'report', expected_revision: 0, routes: [{method: 'POST', path: '/checkout', watch_statuses: [403, 422]}]}});
  assert.deepEqual(gate.routes[0]!.watch_statuses, [403, 422]);
  assert.deepEqual(await AppsService.getRouteHealthReport({slug: 'demo', deployment: 'candidate'}), payload);
});
